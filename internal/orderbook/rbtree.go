package orderbook

// ── Red-Black Tree ───────────────────────────────────────────────────────────
// A balanced binary search tree for O(log n) insert/remove of price levels.
// Uses a sentinel nil node to simplify rotations and fixup.

type rbColor bool

const (
	rbRed   rbColor = true
	rbBlack rbColor = false
)

type rbNode struct {
	price  int64
	level  *PriceLevel
	left   *rbNode
	right  *rbNode
	parent *rbNode
	color  rbColor
}

type rbTree struct {
	root     *rbNode
	sentinel *rbNode            // shared nil sentinel (always black)
	count    int
	index    map[int64]*rbNode // O(1) price lookup
}

func newRBTree() *rbTree {
	sentinel := &rbNode{color: rbBlack}
	return &rbTree{
		root:     sentinel,
		sentinel: sentinel,
		index:    make(map[int64]*rbNode),
	}
}

func (t *rbTree) Len() int {
	return t.count
}

// Find returns the node for the given price, or nil.
func (t *rbTree) Find(price int64) *rbNode {
	n, ok := t.index[price]
	if !ok {
		return nil
	}
	return n
}

// Insert adds a new node with the given price and level. Returns the inserted node.
func (t *rbTree) Insert(price int64, level *PriceLevel) *rbNode {
	z := &rbNode{
		price:  price,
		level:  level,
		left:   t.sentinel,
		right:  t.sentinel,
		color:  rbRed,
	}

	y := t.sentinel
	x := t.root

	for x != t.sentinel {
		y = x
		if price < x.price {
			x = x.left
		} else {
			x = x.right
		}
	}

	z.parent = y
	if y == t.sentinel {
		t.root = z
	} else if price < y.price {
		y.left = z
	} else {
		y.right = z
	}

	t.fixInsert(z)
	t.count++
	t.index[price] = z
	return z
}

// Delete removes the node with the given price.
func (t *rbTree) Delete(price int64) {
	z, ok := t.index[price]
	if !ok {
		return
	}

	delete(t.index, price)
	t.count--

	y := z
	yOrigColor := y.color
	var x *rbNode

	if z.left == t.sentinel {
		x = z.right
		t.transplant(z, z.right)
	} else if z.right == t.sentinel {
		x = z.left
		t.transplant(z, z.left)
	} else {
		y = t.minimum(z.right)
		yOrigColor = y.color
		x = y.right
		if y.parent == z {
			x.parent = y
		} else {
			t.transplant(y, y.right)
			y.right = z.right
			y.right.parent = y
		}
		t.transplant(z, y)
		y.left = z.left
		y.left.parent = y
		y.color = z.color
		// Update index for the moved node
		t.index[y.price] = y
	}

	if yOrigColor == rbBlack {
		t.fixDelete(x)
	}
}

// Min returns the node with the smallest price, or nil.
func (t *rbTree) Min() *rbNode {
	if t.root == t.sentinel {
		return nil
	}
	return t.minimum(t.root)
}

// Max returns the node with the largest price, or nil.
func (t *rbTree) Max() *rbNode {
	if t.root == t.sentinel {
		return nil
	}
	return t.maximum(t.root)
}

// Successor returns the next node in ascending order.
func (t *rbTree) Successor(n *rbNode) *rbNode {
	if n.right != t.sentinel {
		return t.minimum(n.right)
	}
	p := n.parent
	for p != t.sentinel && n == p.right {
		n = p
		p = p.parent
	}
	if p == t.sentinel {
		return nil
	}
	return p
}

// Predecessor returns the previous node in descending order.
func (t *rbTree) Predecessor(n *rbNode) *rbNode {
	if n.left != t.sentinel {
		return t.maximum(n.left)
	}
	p := n.parent
	for p != t.sentinel && n == p.left {
		n = p
		p = p.parent
	}
	if p == t.sentinel {
		return nil
	}
	return p
}

// ForEachAscending calls fn for each node in ascending price order.
// Stops if fn returns false.
func (t *rbTree) ForEachAscending(fn func(*PriceLevel) bool) {
	n := t.Min()
	for n != nil {
		if !fn(n.level) {
			return
		}
		n = t.Successor(n)
	}
}

// ForEachDescending calls fn for each node in descending price order.
// Stops if fn returns false.
func (t *rbTree) ForEachDescending(fn func(*PriceLevel) bool) {
	n := t.Max()
	for n != nil {
		if !fn(n.level) {
			return
		}
		n = t.Predecessor(n)
	}
}

// ── Internal helpers ─────────────────────────────────────────────────────────

func (t *rbTree) minimum(n *rbNode) *rbNode {
	for n.left != t.sentinel {
		n = n.left
	}
	return n
}

func (t *rbTree) maximum(n *rbNode) *rbNode {
	for n.right != t.sentinel {
		n = n.right
	}
	return n
}

func (t *rbTree) rotateLeft(x *rbNode) {
	y := x.right
	x.right = y.left
	if y.left != t.sentinel {
		y.left.parent = x
	}
	y.parent = x.parent
	if x.parent == t.sentinel {
		t.root = y
	} else if x == x.parent.left {
		x.parent.left = y
	} else {
		x.parent.right = y
	}
	y.left = x
	x.parent = y
}

func (t *rbTree) rotateRight(x *rbNode) {
	y := x.left
	x.left = y.right
	if y.right != t.sentinel {
		y.right.parent = x
	}
	y.parent = x.parent
	if x.parent == t.sentinel {
		t.root = y
	} else if x == x.parent.right {
		x.parent.right = y
	} else {
		x.parent.left = y
	}
	y.right = x
	x.parent = y
}

func (t *rbTree) transplant(u, v *rbNode) {
	if u.parent == t.sentinel {
		t.root = v
	} else if u == u.parent.left {
		u.parent.left = v
	} else {
		u.parent.right = v
	}
	v.parent = u.parent
}

func (t *rbTree) fixInsert(z *rbNode) {
	for z.parent.color == rbRed {
		if z.parent == z.parent.parent.left {
			y := z.parent.parent.right
			if y.color == rbRed {
				z.parent.color = rbBlack
				y.color = rbBlack
				z.parent.parent.color = rbRed
				z = z.parent.parent
			} else {
				if z == z.parent.right {
					z = z.parent
					t.rotateLeft(z)
				}
				z.parent.color = rbBlack
				z.parent.parent.color = rbRed
				t.rotateRight(z.parent.parent)
			}
		} else {
			y := z.parent.parent.left
			if y.color == rbRed {
				z.parent.color = rbBlack
				y.color = rbBlack
				z.parent.parent.color = rbRed
				z = z.parent.parent
			} else {
				if z == z.parent.left {
					z = z.parent
					t.rotateRight(z)
				}
				z.parent.color = rbBlack
				z.parent.parent.color = rbRed
				t.rotateLeft(z.parent.parent)
			}
		}
	}
	t.root.color = rbBlack
}

func (t *rbTree) fixDelete(x *rbNode) {
	for x != t.root && x.color == rbBlack {
		if x == x.parent.left {
			w := x.parent.right
			if w.color == rbRed {
				w.color = rbBlack
				x.parent.color = rbRed
				t.rotateLeft(x.parent)
				w = x.parent.right
			}
			if w.left.color == rbBlack && w.right.color == rbBlack {
				w.color = rbRed
				x = x.parent
			} else {
				if w.right.color == rbBlack {
					w.left.color = rbBlack
					w.color = rbRed
					t.rotateRight(w)
					w = x.parent.right
				}
				w.color = x.parent.color
				x.parent.color = rbBlack
				w.right.color = rbBlack
				t.rotateLeft(x.parent)
				x = t.root
			}
		} else {
			w := x.parent.left
			if w.color == rbRed {
				w.color = rbBlack
				x.parent.color = rbRed
				t.rotateRight(x.parent)
				w = x.parent.left
			}
			if w.right.color == rbBlack && w.left.color == rbBlack {
				w.color = rbRed
				x = x.parent
			} else {
				if w.left.color == rbBlack {
					w.right.color = rbBlack
					w.color = rbRed
					t.rotateLeft(w)
					w = x.parent.left
				}
				w.color = x.parent.color
				x.parent.color = rbBlack
				w.left.color = rbBlack
				t.rotateRight(x.parent)
				x = t.root
			}
		}
	}
	x.color = rbBlack
}
