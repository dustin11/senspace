package factory_service

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"senspace/domain/factory"
	"senspace/pkg/merkle"

	"gorm.io/gorm"
)

// 冻结集合包含证明。规范 JSON 字符串提供精确哈希输入，展示文件另行绑定。
type inventoryProofSnapshot struct {
	// 证明协议版本。
	Schema string `json:"schema"`
	// 哈希配对算法。
	Algorithm string `json:"algorithm"`
	// 发布记录。
	ReleaseID string `json:"releaseId"`
	// 铸造后的 token 标识。
	TokenID string `json:"tokenId"`
	// 所属集合。
	CollectionKey string `json:"collectionKey"`
	// 冻结条目编号。
	ItemID string `json:"itemId"`
	// 冻结时的稳定序号。
	ItemIndex int64 `json:"itemIndex"`
	// 条目等级。
	Tier string `json:"tier"`
	// 业务特征哈希。
	TraitHash string `json:"traitHash"`
	// 完整冻结 metadata 的字节哈希。
	MetadataHash string `json:"metadataHash"`
	// 铸造展示文件的字节哈希。
	TokenMetadataHash string `json:"tokenMetadataHash"`
	// 可直接按 UTF-8 哈希的规范 JSON 字符串。
	MetadataCanonical string `json:"metadataCanonical"`
	// 叶子承诺。
	Leaf string `json:"leaf"`
	// 冻结集合根。
	MerkleRoot string `json:"merkleRoot"`
	// 从叶子至根的兄弟节点。
	Proof []string `json:"proof"`
}

// 从已冻结库存恢复包含证明；旧库存继续使用原协议，不隐式改写历史根。
func buildFrozenInventoryProof(asset factory.Asset, tokenMetadataHash string) (*inventoryProofSnapshot, error) {
	if asset.CollectionKey == "" {
		return nil, nil
	}
	tx, err := db()
	if err != nil {
		return nil, err
	}
	var pool factory.NFTInventoryPool
	err = tx.Where("release_id = ? AND collection_key = ?", asset.ReleaseId, asset.CollectionKey).First(&pool).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if asset.TraitHash != "" {
			return nil, newConflictError("资产缺少冻结库存池")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if pool.MerkleRoot == pool.CollectionHash {
		return nil, nil
	}
	var item factory.NFTInventoryItem
	if err := tx.Where("pool_id = ? AND item_id = ?", pool.Id, asset.ItemId).First(&item).Error; err != nil {
		return nil, err
	}
	var release factory.Release
	if err := tx.First(&release, "id = ?", asset.ReleaseId).Error; err != nil {
		return nil, err
	}
	valueTemplate, err := loadReleaseMintTemplate(release)
	if err != nil {
		return nil, err
	}
	for _, collection := range valueTemplate.Collections {
		if collection.Key != item.CollectionKey {
			continue
		}
		ref := resolveCollectionMetadataRef(collection, item.Tier)
		items, err := loadMetadataRefItems(factory.ReleaseStaticDir(release), ref)
		if err != nil {
			return nil, err
		}
		parameters := findTemplateItemById(items, item.ItemId)
		if parameters == nil {
			return nil, newConflictError("冻结库存缺少原始参数")
		}
		return verifyInventoryProof(release, collection, ref, parameters, item, pool.MerkleRoot, asset.TokenId, tokenMetadataHash)
	}
	return nil, newConflictError("发布缺少冻结集合")
}

// 对实际封存参数和库存路径共同验真，任何一方损坏都不能降级为 v1。
func verifyInventoryProof(release factory.Release, collection assetValueCollection, ref string, parameters map[string]any, item factory.NFTInventoryItem, root string, tokenID string, tokenMetadataHash string) (*inventoryProofSnapshot, error) {
	var proof []string
	if err := json.Unmarshal([]byte(item.ProofJson), &proof); err != nil {
		return nil, err
	}
	if !merkle.Verify(item.LeafHash, root, proof) {
		return nil, newConflictError("冻结库存 Merkle 证明不匹配")
	}
	metadata := buildItemNFTMetadata(release, collection, ref, parameters, item.ItemIndex, item.Tier, item.TraitHash)
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	if sha256Hex(data) != item.MetadataHash || hashInventoryLeaf(release, collection.Key, item.ItemIndex, item.ItemId, item.Tier, item.TraitHash, item.MetadataHash) != item.LeafHash {
		return nil, newConflictError("冻结参数与库存承诺不一致")
	}
	return &inventoryProofSnapshot{Schema: "senspace.factory.nft-proof.v2", Algorithm: "sha256-sorted-pairs-duplicate-last", ReleaseID: strconv.FormatInt(release.Id, 10), TokenID: tokenID, CollectionKey: item.CollectionKey, ItemID: item.ItemId, ItemIndex: item.ItemIndex, Tier: item.Tier, TraitHash: item.TraitHash, MetadataHash: item.MetadataHash, TokenMetadataHash: tokenMetadataHash, MetadataCanonical: string(data), Leaf: item.LeafHash, MerkleRoot: root, Proof: proof}, nil
}

// 随发布快照公开库存承诺，客户端应以该根验证单条证明。
func writeInventoryCommitment(stage string, release factory.Release, pools []FreezeReleaseInventoryPool) error {
	return factory.WriteJSONAtomic(filepath.Join(stage, "inventory.json"), map[string]any{"schema": "senspace.inventory-commitment.v1", "releaseId": fmt.Sprint(release.Id), "pools": pools})
}
