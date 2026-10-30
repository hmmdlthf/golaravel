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

			if n.wildChild {
				n = n.children[0]
				n.priority++

				// Check if the wildcard matches
				if len(path) >= len(n.path) && n.path == path[:len(n.path)] {
					if len(n.path) >= len(path) || path[len(n.path)] == '/' {
						continue walk
					}
				}
				panic("wildcard conflict in path '" + fullPath + "'")
			}

			c := path[0]

			// Check if a child with the next path byte exists
			for j := 0; j < len(n.indices); j++ {
				if c == n.indices[j] {
					j = n.incrementPriority(j)
					n = n.children[j]
					continue walk
				}
			}

			// Otherwise insert it
			if c != ':' && c != '*' {
				n.indices += string([]byte{c})
				child := &node{}
				n.children = append(n.children, child)
				n.incrementPriority(len(n.indices) - 1)
				n = child
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
			n.children = []*node{child}
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

		if len(n.path) > 0 && n.path[len(n.path)-1] == '/' {
			panic("catch-all conflict with existing wildcard in path '" + fullPath + "'")
		}

		i--
		if path[i] != '/' {
			panic("no / before catch-all in path '" + fullPath + "'")
		}

		n.path = path[:i]

		// First node: child with only the invalid path character
		child := &node{
			wildChild: true,
			nType:     catchAllNode,
		}
		n.children = []*node{child}
		n.indices = string('/')
		n = child
		n.priority++

		// Second node: child containing the variable
		child = &node{
			path:      path[i:],
			nType:     catchAllNode,
			handlers:  handlers,
			priority:  1,
			paramName: wildcard[1:],
		}
		n.children = []*node{child}
		return
	}
}

// getValue returns the handlers registered with the given path and populates context params.
func (n *node) getValue(path string, c *context.Context) []context.HandlerFunc {
walk:
	for {
		prefix := n.path
		if len(path) > len(prefix) {
			if path[:len(prefix)] == prefix {
				path = path[len(prefix):]

				// If this node does not have a wildcard child, search next static child
				if !n.wildChild {
					cByte := path[0]
					for i := 0; i < len(n.indices); i++ {
						if cByte == n.indices[i] {
							n = n.children[i]
							continue walk
						}
					}
					// Nothing found
					return nil
				}

				// Handle wildcard child
				n = n.children[0]
				switch n.nType {
				case paramNode:
					// Find end of parameter (delimited by '/')
					end := 0
					for end < len(path) && path[end] != '/' {
						end++
					}

					// Add parameter directly to context inline buffer
					if c != nil {
						c.AddParam(n.paramName, path[:end])
					}

					// We need to go deeper!
					if end < len(path) {
						if len(n.children) > 0 {
							path = path[end:]
							n = n.children[0]
							continue walk
						}
						return nil
					}

					if n.handlers != nil {
						return n.handlers
					}
					return nil

				case catchAllNode:
					if c != nil {
						c.AddParam(n.paramName, path)
					}
					return n.handlers

				default:
					panic("invalid node type")
				}
			}
		} else if path == prefix {
			// Exact match
			if n.handlers != nil {
				return n.handlers
			}
		}
		return nil
	}
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
