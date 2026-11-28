package router

import (
	"github.com/golaravel/golaravel/pkg/context"
)

type nodeType uint8

const (
	staticNode nodeType = iota
	paramNode
	catchAllNode
)

type node struct {
	path      string
	indices   string
	wildChild bool
	nType     nodeType
	priority  uint32
	children  []*node
	handlers  []context.HandlerFunc
	paramName string
}

func min(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

func countParams(path string) uint16 {
	var n uint16
	for i := 0; i < len(path); i++ {
		if path[i] == ':' || path[i] == '*' {
			n++
		}
	}
	return n
}

func (n *node) incrementPriority(pos int) int {
	n.children[pos].priority++
	prio := n.children[pos].priority

	// Adjust position (bubble up) to maintain max priority first
	newPos := pos
	for newPos > 0 && n.children[newPos-1].priority < prio {
		// Swap
		n.children[newPos-1], n.children[newPos] = n.children[newPos], n.children[newPos-1]
		newPos--
	}

	// Build new indices string
	if newPos != pos {
		indices := []byte(n.indices)
		char := indices[pos]
		copy(indices[newPos+1:pos+1], indices[newPos:pos])
		indices[newPos] = char
		n.indices = string(indices)
	}

	return newPos
}

// addStaticChild inserts a static child and returns its position.
//
// Invariant: children[0:len(indices)] are the static children, in the order
// of indices, and any wildcard (param / catch-all) child comes after them.
// Appending a static child after a wildcard child would make indices[i] point
// at the wrong child, so static routes registered after a sibling parameter
// route (e.g. "/reorder" after "/:id/...") would never match.
func (n *node) addStaticChild(c byte, child *node) int {
	pos := len(n.indices)
	n.indices += string([]byte{c})
	n.children = append(n.children, nil)
	copy(n.children[pos+1:], n.children[pos:])
	n.children[pos] = child
	return pos
}

// addRoute adds a node with the given handle to the path.
func (n *node) addRoute(path string, handlers []context.HandlerFunc) {
	fullPath := path
	n.priority++

	// Empty tree
	if len(n.path) == 0 && len(n.children) == 0 {
		n.insertChild(path, fullPath, handlers)
		n.nType = staticNode
		return
	}

walk:
	for {
		// Find the longest common prefix
		i := 0
		max := min(len(path), len(n.path))
		for i < max && path[i] == n.path[i] {
			i++
		}

		// Split edge if common prefix is shorter than current node's path
		if i < len(n.path) {
			child := node{
				path:      n.path[i:],
				wildChild: n.wildChild,
				nType:     staticNode,
				indices:   n.indices,
				children:  n.children,
				handlers:  n.handlers,
				priority:  n.priority - 1,
			}

			n.children = []*node{&child}
			n.indices = string([]byte{n.path[i]})
			n.path = path[:i]
			n.handlers = nil
			n.wildChild = false
		}

		// Make new node a child of this node
		if i < len(path) {
			path = path[i:]
			c := path[0]

			// Check if a static child with the next path byte exists
			for j := 0; j < len(n.indices); j++ {
				if c == n.indices[j] {
					j = n.incrementPriority(j)
					n = n.children[j]
					continue walk
				}
			}

			// If adding a static child
			if c != ':' && c != '*' {
				child := &node{}
				n.incrementPriority(n.addStaticChild(c, child))
				n = child
				n.insertChild(path, fullPath, handlers)
				return
			}

			if n.wildChild {
				// find wildcard child
				for _, ch := range n.children {
					if ch.nType == paramNode || ch.nType == catchAllNode {
						n = ch
						break
					}
				}
				n.priority++

				// Check if the wildcard matches
				if len(path) >= len(n.path) && n.path == path[:len(n.path)] {
					if len(n.path) >= len(path) || path[len(n.path)] == '/' {
						continue walk
					}
				}
				panic("wildcard conflict in path '" + fullPath + "'")
			}

			n.insertChild(path, fullPath, handlers)
			return
		}

		// Otherwise add handlers to current node
		if n.handlers != nil {
			panic("handlers are already registered for path '" + fullPath + "'")
		}
		n.handlers = handlers
		return
	}
}

func (n *node) insertChild(path, fullPath string, handlers []context.HandlerFunc) {
	for {
		// Find wildcard prefix
		wildcard, i, valid := findWildcard(path)
		if i < 0 {
			// No wildcard found, add as static
			n.path = path
			n.handlers = handlers
			return
		}

		if !valid {
			panic("only one wildcard per path segment is allowed, path: " + fullPath)
		}

		// Check if the wildcard has a name
		if len(wildcard) < 2 {
			panic("wildcards must be named with non-empty name in path '" + fullPath + "'")
		}

		if wildcard[0] == ':' { // param
			if i > 0 {
				n.path = path[:i]
				path = path[i:]
			}

			child := &node{
				nType:     paramNode,
				path:      wildcard,
				paramName: wildcard[1:],
			}
			n.children = append(n.children, child)
			n.wildChild = true
			n = child
			n.priority++

			// If the path doesn't end with the wildcard, add another static child
			if len(wildcard) < len(path) {
				path = path[len(wildcard):]
				child := &node{
					priority: 1,
				}
				n.children = []*node{child}
				// index the child, so later routes under the same
				// parameter walk into it instead of adding a duplicate
				// sibling that shadows it
				n.indices = string([]byte{path[0]})
				n = child
				continue
			}

			// Otherwise we're done
			n.handlers = handlers
			return
		}

		// catch-all wildcard ('*')
		if i+len(wildcard) != len(path) {
			panic("catch-all routes are only allowed at the end of the path in '" + fullPath + "'")
		}

		if i > 0 {
			if path[i-1] != '/' {
				panic("no / before catch-all in path '" + fullPath + "'")
			}
			if len(n.path) == 0 {
				n.path = path[:i]
			} else {
				child := &node{
					path:     path[:i],
					priority: 1,
				}
				n.addStaticChild(path[0], child)
				n = child
			}
			path = path[i:]
		} else {
			if len(n.path) == 0 || n.path[len(n.path)-1] != '/' {
				panic("no / before catch-all in path '" + fullPath + "'")
			}
		}

		for _, ch := range n.children {
			if ch.nType == catchAllNode {
				panic("catch-all conflict with existing wildcard in path '" + fullPath + "'")
			}
		}

		child := &node{
			path:      wildcard,
			nType:     catchAllNode,
			handlers:  handlers,
			priority:  1,
			paramName: wildcard[1:],
		}
		n.children = append(n.children, child)
		n.wildChild = true
		return
	}
}

// getValue returns the handlers registered with the given path and populates context params.
func (n *node) getValue(path string, c *context.Context) []context.HandlerFunc {
	prefix := n.path
	if len(path) > len(prefix) {
		if path[:len(prefix)] == prefix {
			subPath := path[len(prefix):]

			// 1. Try static children first
			cByte := subPath[0]
			for i := 0; i < len(n.indices); i++ {
				if cByte == n.indices[i] {
					if handlers := n.children[i].getValue(subPath, c); handlers != nil {
						return handlers
					}
					break
				}
			}

			// 2. Fall back to wildcard child if static child did not produce a match
			if n.wildChild {
				for _, ch := range n.children {
					if ch.nType == paramNode {
						end := 0
						for end < len(subPath) && subPath[end] != '/' {
							end++
						}
						if c != nil {
							c.AddParam(ch.paramName, subPath[:end])
						}
						if end < len(subPath) {
							rest := subPath[end:]
							for _, subCh := range ch.children {
								if len(rest) >= len(subCh.path) && rest[:len(subCh.path)] == subCh.path {
									if handlers := subCh.getValue(rest, c); handlers != nil {
										return handlers
									}
								}
							}
							if len(ch.children) > 0 {
								if handlers := ch.children[0].getValue(rest, c); handlers != nil {
									return handlers
								}
							}
						} else if ch.handlers != nil {
							return ch.handlers
						}
					} else if ch.nType == catchAllNode {
						if c != nil {
							c.AddParam(ch.paramName, subPath)
						}
						return ch.handlers
					}
				}
			}
		}
	} else if path == prefix {
		if n.handlers != nil {
			return n.handlers
		}
		if n.wildChild {
			for _, ch := range n.children {
				if ch.nType == catchAllNode {
					if c != nil {
						c.AddParam(ch.paramName, "")
					}
					return ch.handlers
				}
			}
		}
	}
	return nil
}

// findWildcard finds the first parameter or catch-all wildcard in a path segment.
func findWildcard(path string) (wildcard string, i int, valid bool) {
	for start, c := range []byte(path) {
		if c != ':' && c != '*' {
			continue
		}

		valid = true
		for end, c := range []byte(path[start+1:]) {
			switch c {
			case '/':
				return path[start : start+1+end], start, valid
			case ':', '*':
				valid = false
			}
		}
		return path[start:], start, valid
	}
	return "", -1, false
}
