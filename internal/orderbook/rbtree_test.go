package orderbook

import (
	"math/rand"
	"strings"
	"testing"
)

func TestPriceLevelUpdateQtyUnderflowPanics(t *testing.T) {
	pl := newPriceLevel(50000)
	pl.TotalQty = 100

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on underflow, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "underflow") {
			t.Errorf("panic value = %v, want substring 'underflow'", r)
		}
	}()

	pl.UpdateQty(1, -200) // delta magnitude > TotalQty
}

func TestPriceLevelUpdateQtyHappyPath(t *testing.T) {
	pl := newPriceLevel(50000)
	pl.TotalQty = 100

	pl.UpdateQty(1, -40)
	if pl.TotalQty != 60 {
		t.Errorf("TotalQty = %d, want 60", pl.TotalQty)
	}
	pl.UpdateQty(1, 25)
	if pl.TotalQty != 85 {
		t.Errorf("TotalQty = %d, want 85", pl.TotalQty)
	}
}

func TestRBTreeInsertAndFind(t *testing.T) {
	tree := newRBTree()
	for i := 0; i < 100; i++ {
		pl := newPriceLevel(int64(i * 100))
		tree.Insert(int64(i*100), pl)
	}
	if tree.Len() != 100 {
		t.Errorf("Len() = %d, want 100", tree.Len())
	}
	for i := 0; i < 100; i++ {
		n := tree.Find(int64(i * 100))
		if n == nil {
			t.Fatalf("Find(%d) returned nil", i*100)
		}
		if n.level.Price != int64(i*100) {
			t.Errorf("Find(%d).Price = %d", i*100, n.level.Price)
		}
	}
	if tree.Find(999) != nil {
		t.Error("Find(999) should return nil")
	}
}

func TestRBTreeDelete(t *testing.T) {
	tree := newRBTree()
	for i := 0; i < 50; i++ {
		tree.Insert(int64(i), newPriceLevel(int64(i)))
	}

	// Delete every other
	for i := 0; i < 50; i += 2 {
		tree.Delete(int64(i))
	}
	if tree.Len() != 25 {
		t.Errorf("Len() = %d, want 25", tree.Len())
	}

	// Verify deleted nodes are gone
	for i := 0; i < 50; i += 2 {
		if tree.Find(int64(i)) != nil {
			t.Errorf("Find(%d) should be nil after delete", i)
		}
	}
	// Verify remaining nodes still exist
	for i := 1; i < 50; i += 2 {
		if tree.Find(int64(i)) == nil {
			t.Errorf("Find(%d) should still exist", i)
		}
	}
}

func TestRBTreeDeleteNonexistent(t *testing.T) {
	tree := newRBTree()
	tree.Insert(100, newPriceLevel(100))
	tree.Delete(999) // should not panic
	if tree.Len() != 1 {
		t.Errorf("Len() = %d", tree.Len())
	}
}

func TestRBTreeMinMax(t *testing.T) {
	tree := newRBTree()
	if tree.Min() != nil {
		t.Error("Min() should be nil on empty tree")
	}
	if tree.Max() != nil {
		t.Error("Max() should be nil on empty tree")
	}

	prices := []int64{50, 20, 80, 10, 30, 60, 90}
	for _, p := range prices {
		tree.Insert(p, newPriceLevel(p))
	}

	if tree.Min().price != 10 {
		t.Errorf("Min() = %d, want 10", tree.Min().price)
	}
	if tree.Max().price != 90 {
		t.Errorf("Max() = %d, want 90", tree.Max().price)
	}
}

func TestRBTreeSuccessorPredecessor(t *testing.T) {
	tree := newRBTree()
	for i := 1; i <= 5; i++ {
		tree.Insert(int64(i*10), newPriceLevel(int64(i*10)))
	}
	// 10, 20, 30, 40, 50

	n := tree.Min() // 10
	if n.price != 10 {
		t.Fatalf("Min = %d", n.price)
	}

	s := tree.Successor(n) // 20
	if s == nil || s.price != 20 {
		t.Fatalf("Successor(10) = %v", s)
	}
	s = tree.Successor(s) // 30
	if s == nil || s.price != 30 {
		t.Fatalf("Successor(20) = %v", s)
	}

	mx := tree.Max() // 50
	if tree.Successor(mx) != nil {
		t.Error("Successor of max should be nil")
	}

	p := tree.Predecessor(mx) // 40
	if p == nil || p.price != 40 {
		t.Fatalf("Predecessor(50) = %v", p)
	}
	if tree.Predecessor(tree.Min()) != nil {
		t.Error("Predecessor of min should be nil")
	}
}

func TestRBTreeForEachAscending(t *testing.T) {
	tree := newRBTree()
	prices := []int64{30, 10, 50, 20, 40}
	for _, p := range prices {
		tree.Insert(p, newPriceLevel(p))
	}

	var collected []int64
	tree.ForEachAscending(func(pl *PriceLevel) bool {
		collected = append(collected, pl.Price)
		return true
	})

	expected := []int64{10, 20, 30, 40, 50}
	if len(collected) != len(expected) {
		t.Fatalf("collected %d, want %d", len(collected), len(expected))
	}
	for i, v := range collected {
		if v != expected[i] {
			t.Errorf("collected[%d] = %d, want %d", i, v, expected[i])
		}
	}
}

func TestRBTreeForEachDescending(t *testing.T) {
	tree := newRBTree()
	for i := 1; i <= 5; i++ {
		tree.Insert(int64(i*10), newPriceLevel(int64(i*10)))
	}

	var collected []int64
	tree.ForEachDescending(func(pl *PriceLevel) bool {
		collected = append(collected, pl.Price)
		return true
	})

	expected := []int64{50, 40, 30, 20, 10}
	for i, v := range collected {
		if v != expected[i] {
			t.Errorf("collected[%d] = %d, want %d", i, v, expected[i])
		}
	}
}

func TestRBTreeForEachEarlyStop(t *testing.T) {
	tree := newRBTree()
	for i := 1; i <= 10; i++ {
		tree.Insert(int64(i), newPriceLevel(int64(i)))
	}

	count := 0
	tree.ForEachAscending(func(pl *PriceLevel) bool {
		count++
		return count < 3
	})
	if count != 3 {
		t.Errorf("early stop: count = %d, want 3", count)
	}
}

// ── RB-tree property tests ───────────────────────────────────────────────────

func TestRBTreePropertyRootIsBlack(t *testing.T) {
	tree := newRBTree()
	for i := 0; i < 1000; i++ {
		tree.Insert(int64(rand.Intn(100000)), newPriceLevel(int64(i)))
	}
	if tree.root != tree.sentinel && tree.root.color != rbBlack {
		t.Error("root must be black")
	}
}

func TestRBTreePropertyNoConsecutiveReds(t *testing.T) {
	tree := newRBTree()
	for i := 0; i < 1000; i++ {
		tree.Insert(int64(rand.Intn(100000)), newPriceLevel(int64(i)))
	}

	var checkNoDoubleRed func(n *rbNode)
	checkNoDoubleRed = func(n *rbNode) {
		if n == tree.sentinel {
			return
		}
		if n.color == rbRed {
			if n.left.color == rbRed {
				t.Errorf("consecutive red nodes at price %d (left child)", n.price)
			}
			if n.right.color == rbRed {
				t.Errorf("consecutive red nodes at price %d (right child)", n.price)
			}
		}
		checkNoDoubleRed(n.left)
		checkNoDoubleRed(n.right)
	}
	checkNoDoubleRed(tree.root)
}

func TestRBTreePropertyBlackHeight(t *testing.T) {
	tree := newRBTree()
	for i := 0; i < 1000; i++ {
		tree.Insert(int64(rand.Intn(100000)), newPriceLevel(int64(i)))
	}

	var blackHeight func(n *rbNode) int
	blackHeight = func(n *rbNode) int {
		if n == tree.sentinel {
			return 1
		}
		lh := blackHeight(n.left)
		rh := blackHeight(n.right)
		if lh != rh {
			t.Errorf("unequal black height at price %d: left=%d right=%d", n.price, lh, rh)
			return -1
		}
		if n.color == rbBlack {
			return lh + 1
		}
		return lh
	}
	blackHeight(tree.root)
}

func TestRBTreeStressInsertDelete(t *testing.T) {
	tree := newRBTree()

	// Insert 5000 random prices
	prices := make([]int64, 5000)
	for i := range prices {
		prices[i] = int64(rand.Intn(1000000))
	}
	for _, p := range prices {
		if tree.Find(p) == nil {
			tree.Insert(p, newPriceLevel(p))
		}
	}

	initialCount := tree.Len()

	// Delete half
	for i := 0; i < len(prices)/2; i++ {
		tree.Delete(prices[i])
	}

	// Verify tree is still valid
	if tree.root != tree.sentinel && tree.root.color != rbBlack {
		t.Error("root must be black after deletes")
	}
	if tree.Len() < 0 || tree.Len() > initialCount {
		t.Errorf("invalid Len %d after deletes", tree.Len())
	}
}
