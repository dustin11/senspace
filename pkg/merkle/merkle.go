// Package merkle 使用 SHA-256 和排序字节对构建集合包含证明。
package merkle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Build 返回根及每个叶子的兄弟路径；奇数层复制末尾叶子，路径从叶子向根排列。
func Build(leaves []string) (string, [][]string, error) {
	if len(leaves) == 0 {
		return "", nil, fmt.Errorf("Merkle 集合不能为空")
	}
	level := make([][]byte, len(leaves))
	for i, leaf := range leaves {
		value, err := decode(leaf)
		if err != nil {
			return "", nil, err
		}
		level[i] = value
	}
	levels := [][][]byte{level}
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			j := i + 1
			if j == len(level) {
				j = i
			}
			next = append(next, pair(level[i], level[j]))
		}
		level = next
		levels = append(levels, level)
	}
	proofs := make([][]string, len(leaves))
	for i := range leaves {
		proofs[i] = []string{}
		index := i
		for _, nodes := range levels[:len(levels)-1] {
			sibling := index ^ 1
			if sibling >= len(nodes) {
				sibling = index
			}
			proofs[i] = append(proofs[i], hex.EncodeToString(nodes[sibling]))
			index /= 2
		}
	}
	return hex.EncodeToString(level[0]), proofs, nil
}

// Verify 验证完整叶子哈希是否属于指定集合根。
func Verify(leaf string, root string, proof []string) bool {
	value, err := decode(leaf)
	if err != nil {
		return false
	}
	expected, err := decode(root)
	if err != nil {
		return false
	}
	for _, sibling := range proof {
		other, err := decode(sibling)
		if err != nil {
			return false
		}
		value = pair(value, other)
	}
	return bytes.Equal(value, expected)
}

// 解码固定长度哈希。
func decode(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, fmt.Errorf("Merkle 节点必须是 SHA-256 哈希")
	}
	return decoded, nil
}

// 排序原始字节后计算父节点。
func pair(a []byte, b []byte) []byte {
	if bytes.Compare(a, b) > 0 {
		a, b = b, a
	}
	var input [sha256.Size * 2]byte
	copy(input[:sha256.Size], a)
	copy(input[sha256.Size:], b)
	hash := sha256.Sum256(input[:])
	return hash[:]
}
