package graphrag

import (
	"math"
	"testing"
)

func TestRunPPR_MultiHopPropagation(t *testing.T) {
	// Build a 2-hop chain:
	//   E0 (seed) -- E1 (bridge) -- E2 (target) -- C_target
	//   E0 -- C_direct
	//   E_unrelated -- C_unrelated
	g := NewGraph(8)
	e0 := g.AddNode(NodeEntity, "e0")
	e1 := g.AddNode(NodeEntity, "e1")
	e2 := g.AddNode(NodeEntity, "e2")
	cDirect := g.AddNode(NodeChunk, "c_direct")
	cTarget := g.AddNode(NodeChunk, "c_target")
	eUnrel := g.AddNode(NodeEntity, "e_unrel")
	cUnrel := g.AddNode(NodeChunk, "c_unrel")

	g.AddUndirectedEdge(e0, e1, 1.0)
	g.AddUndirectedEdge(e1, e2, 1.0)
	g.AddUndirectedEdge(e2, cTarget, 1.0)
	g.AddUndirectedEdge(e0, cDirect, 1.0)
	g.AddUndirectedEdge(eUnrel, cUnrel, 1.0)

	scores, iters := RunPPR(g, map[int]float64{e0: 1.0}, DefaultPPRConfig())
	if iters == 0 {
		t.Fatal("expected at least 1 iteration")
	}

	// Probability mass should sum to 1.0.
	sum := 0.0
	for _, s := range scores {
		sum += s
	}
	if math.Abs(sum-1.0) > 1e-6 {
		t.Fatalf("PPR scores sum = %.8f, want 1.0", sum)
	}

	// Connected 2-hop chunk must outrank disconnected chunk (which gets 0 mass).
	if scores[cTarget] <= 0 {
		t.Fatalf("expected c_target score > 0, got %v", scores[cTarget])
	}
	if scores[cUnrel] != 0 {
		t.Fatalf("expected disconnected c_unrel score == 0, got %v", scores[cUnrel])
	}
	if scores[e0] <= scores[e1] || scores[e1] <= scores[e2] {
		t.Fatalf("expected monotonic decay along hop chain: e0=%v e1=%v e2=%v", scores[e0], scores[e1], scores[e2])
	}
}

func TestRunPPR_DanglingNodeMassPreserved(t *testing.T) {
	g := NewGraph(4)
	a := g.AddNode(NodeEntity, "a")
	b := g.AddNode(NodeEntity, "b")
	_ = b // dangling isolated node

	scores, _ := RunPPR(g, map[int]float64{a: 0.7, b: 0.3}, DefaultPPRConfig())
	sum := scores[a] + scores[b]
	if math.Abs(sum-1.0) > 1e-6 {
		t.Fatalf("expected total probability mass 1.0 with dangling nodes, got %.8f", sum)
	}
	if math.Abs(scores[a]-0.7) > 1e-6 || math.Abs(scores[b]-0.3) > 1e-6 {
		t.Fatalf("expected scores to match personalization on isolated nodes: a=%.6f b=%.6f", scores[a], scores[b])
	}
}

func TestRunPPR_DualSeedConvergence(t *testing.T) {
	// Two seed entities E_a and E_b both connect to bridge entity E_mid -> C_bridge,
	// while E_a also connects to distractor C_a and E_b to distractor C_b.
	// C_bridge receives mass from BOTH seeds and should outrank distractors.
	g := NewGraph(8)
	ea := g.AddNode(NodeEntity, "ea")
	eb := g.AddNode(NodeEntity, "eb")
	emid := g.AddNode(NodeEntity, "emid")
	cBridge := g.AddNode(NodeChunk, "c_bridge")
	cA := g.AddNode(NodeChunk, "c_a")
	cB := g.AddNode(NodeChunk, "c_b")

	g.AddUndirectedEdge(ea, emid, 1.5)
	g.AddUndirectedEdge(eb, emid, 1.5)
	g.AddUndirectedEdge(emid, cBridge, 1.0)
	g.AddUndirectedEdge(ea, cA, 0.5)
	g.AddUndirectedEdge(eb, cB, 0.5)

	scores, _ := RunPPR(g, map[int]float64{ea: 0.5, eb: 0.5}, DefaultPPRConfig())
	if scores[cBridge] <= scores[cA] || scores[cBridge] <= scores[cB] {
		t.Fatalf("expected c_bridge (%v) to beat single-seed distractors c_a (%v), c_b (%v)",
			scores[cBridge], scores[cA], scores[cB])
	}
}
