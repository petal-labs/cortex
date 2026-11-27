// Package graphrag implements zero-LLM-at-query-time HippoRAG-style
// Vector-Graph retrieval over Cortex's knowledge chunks, entities, and
// relationships using pure-Go Personalized PageRank (PPR).
package graphrag

import "math"

// NodeKind distinguishes entity nodes from chunk (passage) nodes in the
// local bipartite retrieval graph.
type NodeKind uint8

const (
	NodeEntity NodeKind = iota
	NodeChunk
)

// Node is a vertex in the compact local retrieval graph.
type Node struct {
	ID   string
	Kind NodeKind
}

// Edge is a weighted undirected neighbor entry in the adjacency list.
type Edge struct {
	To     int
	Weight float64
}

// Graph is a compact, integer-indexed undirected weighted graph built per
// query from the bounded 1-2 hop neighborhood around seed entities and chunks.
type Graph struct {
	idToIdx map[string]int
	nodes   []Node
	adj     [][]Edge
	edgeCnt int
}

// NewGraph allocates an empty local retrieval graph.
func NewGraph(capacityHint int) *Graph {
	if capacityHint < 16 {
		capacityHint = 16
	}
	return &Graph{
		idToIdx: make(map[string]int, capacityHint),
		nodes:   make([]Node, 0, capacityHint),
		adj:     make([][]Edge, 0, capacityHint),
	}
}

func nodeKey(kind NodeKind, id string) string {
	if kind == NodeEntity {
		return "e:" + id
	}
	return "c:" + id
}

// AddNode inserts or returns the index of a node in the graph.
func (g *Graph) AddNode(kind NodeKind, id string) int {
	k := nodeKey(kind, id)
	if idx, ok := g.idToIdx[k]; ok {
		return idx
	}
	idx := len(g.nodes)
	g.idToIdx[k] = idx
	g.nodes = append(g.nodes, Node{ID: id, Kind: kind})
	g.adj = append(g.adj, nil)
	return idx
}

// Lookup returns the node index if present.
func (g *Graph) Lookup(kind NodeKind, id string) (int, bool) {
	idx, ok := g.idToIdx[nodeKey(kind, id)]
	return idx, ok
}

// Nodes returns all nodes in index order.
func (g *Graph) Nodes() []Node {
	return g.nodes
}

// NodeCount returns the number of vertices in the graph.
func (g *Graph) NodeCount() int {
	return len(g.nodes)
}

// EdgeCount returns the number of undirected edges added to the graph.
func (g *Graph) EdgeCount() int {
	return g.edgeCnt
}

// AddUndirectedEdge adds positive weight between u and v in both directions.
// Parallel edges between the same endpoints accumulate weight.
func (g *Graph) AddUndirectedEdge(u, v int, weight float64) {
	if u == v || weight <= 0 || u < 0 || v < 0 || u >= len(g.nodes) || v >= len(g.nodes) {
		return
	}
	added := addHalfEdge(&g.adj[u], v, weight)
	addHalfEdge(&g.adj[v], u, weight)
	if added {
		g.edgeCnt++
	}
}

func addHalfEdge(edges *[]Edge, to int, weight float64) bool {
	for i := range *edges {
		if (*edges)[i].To == to {
			(*edges)[i].Weight += weight
			return false
		}
	}
	*edges = append(*edges, Edge{To: to, Weight: weight})
	return true
}

// PPRConfig controls Personalized PageRank power iteration.
type PPRConfig struct {
	Damping       float64 // Teleportation complement (default: 0.85)
	MaxIterations int     // Maximum power-iteration steps (default: 20)
	Tolerance     float64 // L1 convergence threshold (default: 1e-6)
}

// DefaultPPRConfig returns standard HippoRAG PPR parameters.
func DefaultPPRConfig() PPRConfig {
	return PPRConfig{
		Damping:       0.85,
		MaxIterations: 20,
		Tolerance:     1e-6,
	}
}

// RunPPR computes Personalized PageRank scores over g given a non-negative
// personalization map (node index -> unnormalized weight). Returns the score
// slice aligned with g.Nodes() and the number of iterations executed.
func RunPPR(g *Graph, personalization map[int]float64, cfg PPRConfig) ([]float64, int) {
	n := len(g.nodes)
	if n == 0 {
		return nil, 0
	}

	damping := cfg.Damping
	if damping <= 0 || damping >= 1 {
		damping = 0.85
	}
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	tol := cfg.Tolerance
	if tol <= 0 {
		tol = 1e-6
	}

	// Build normalized teleport vector p.
	p := make([]float64, n)
	pSum := 0.0
	for idx, w := range personalization {
		if idx >= 0 && idx < n && w > 0 {
			p[idx] += w
			pSum += w
		}
	}
	if pSum == 0 {
		uniform := 1.0 / float64(n)
		for i := range p {
			p[i] = uniform
		}
	} else {
		inv := 1.0 / pSum
		for i := range p {
			p[i] *= inv
		}
	}

	// Precompute weighted out-degree for every node.
	outWeight := make([]float64, n)
	for u := range n {
		sum := 0.0
		for _, e := range g.adj[u] {
			sum += e.Weight
		}
		outWeight[u] = sum
	}

	// Initialize rank vector to teleport distribution.
	rank := make([]float64, n)
	copy(rank, p)
	next := make([]float64, n)

	iters := 0
	teleportFactor := 1.0 - damping

	for iter := 1; iter <= maxIter; iter++ {
		iters = iter
		for i := range next {
			next[i] = teleportFactor * p[i]
		}

		danglingMass := 0.0
		for u := range n {
			ru := rank[u]
			if ru == 0 {
				continue
			}
			ow := outWeight[u]
			if ow == 0 {
				danglingMass += ru
				continue
			}
			scale := damping * ru / ow
			for _, e := range g.adj[u] {
				next[e.To] += scale * e.Weight
			}
		}

		// Standard PPR dangling redistribution follows the personalization vector.
		if danglingMass > 0 {
			dScale := damping * danglingMass
			for i := range next {
				if p[i] > 0 {
					next[i] += dScale * p[i]
				}
			}
		}

		// L1 convergence check.
		diff := 0.0
		for i := range n {
			diff += math.Abs(next[i] - rank[i])
		}
		rank, next = next, rank
		if diff < tol {
			break
		}
	}

	return rank, iters
}
