package merkle

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// 覆盖奇数层、单叶和正式集合规模，并验证参数或路径变化后拒绝证明。
func TestMembershipAndTampering(t *testing.T) {
	for _, count := range []int{1, 2, 3, 7, 60000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			leaves := make([]string, count)
			for i := range leaves {
				leaves[i] = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(i))))
			}
			root, proofs, err := Build(leaves)
			if err != nil {
				t.Fatal(err)
			}
			for i, leaf := range leaves {
				if !Verify(leaf, root, proofs[i]) {
					t.Fatalf("第 %d 条证明失败", i)
				}
			}
			changed := fmt.Sprintf("%x", sha256.Sum256([]byte("changed")))
			if Verify(changed, root, proofs[0]) {
				t.Fatal("接受了篡改的叶子")
			}
			if len(proofs[0]) > 0 {
				proofs[0][0] = changed
				if Verify(leaves[0], root, proofs[0]) {
					t.Fatal("接受了篡改的路径")
				}
			}
		})
	}
	if _, _, err := Build(nil); err == nil {
		t.Fatal("接受了空集合")
	}
	if _, _, err := Build([]string{"bad"}); err == nil {
		t.Fatal("接受了非法叶子")
	}
}
