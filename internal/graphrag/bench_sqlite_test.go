package graphrag

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
)

// TestSQLite25KBenchmark populates a real file-backed SQLite WAL database at
// Cortex's 25,000-chunk SQLite target ceiling (1536-dim float32 vectors) and
// benchmarks Baseline Hybrid vs production Variant B (relationships_fts FTS5)
// under both idle reads and concurrent background write load.
//
// Run explicitly:
//
//	CORTEX_RUN_25K_BENCH=1 go test -tags sqlite_fts5 -v -run TestSQLite25KBenchmark -timeout 15m ./internal/graphrag/...
func TestSQLite25KBenchmark(t *testing.T) {
	if testing.Short() || os.Getenv("CORTEX_RUN_25K_BENCH") != "1" {
		t.Skip("set CORTEX_RUN_25K_BENCH=1 to run the 25K-chunk SQLite benchmark")
	}

	const (
		ns          = "bench-25k"
		colID       = "col-25k"
		dim         = 1536
		numChunks   = 25_000
		numEntities = 15_000
		numRels     = 35_000
		numMentions = 75_000
		numQueries  = 20
		warmupRuns  = 2
	)

	tmpDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.Backend = "sqlite"
	cfg.Storage.DataDir = tmpDir
	cfg.Embedding.Dimensions = dim

	backend, err := sqlite.New(cfg)
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()
	t.Logf("SQLite FTS5 available: %v", backend.FTS5Available(ctx))

	popStart := time.Now()
	if err := populate25KFixture(ctx, backend.DB(), ns, colID, dim, numChunks, numEntities, numRels, numMentions); err != nil {
		t.Fatalf("populate25KFixture failed: %v", err)
	}
	t.Logf("Populated 25K SQLite fixture (%d chunks, %d entities, %d rels, %d mentions, dim=%d) in %s",
		numChunks, numEntities, numRels, numMentions, dim, time.Since(popStart).Round(time.Millisecond))

	_, _ = backend.DB().ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")

	dbPath := filepath.Join(tmpDir, "cortex.db")
	dbSizeMB := fileSizeMB(dbPath)
	t.Logf("Total SQLite DB size (Variant B production schema): %.1f MB", dbSizeMB)

	rng := rand.New(rand.NewPCG(42, 99))
	type benchQuery struct {
		text string
		vec  []float32
	}
	queries := make([]benchQuery, numQueries)
	for i := range numQueries {
		cluster := (i * 7) % 100
		svcName := fmt.Sprintf("Service-%04d", cluster*10)
		dbName := fmt.Sprintf("Datastore-%04d", cluster*10+2)
		queries[i] = benchQuery{
			text: fmt.Sprintf("Who manages %s and which vendor hosts %s?", svcName, dbName),
			vec:  makeClusterVector(rng, cluster, dim, 0.15),
		}
	}

	retriever := NewRetriever(backend)

	type latencyReport struct {
		name      string
		p50       time.Duration
		p95       time.Duration
		avgSeed   time.Duration
		avgExpand time.Duration
		avgPPR    time.Duration
		avgHydr   time.Duration
	}

	runBench := func(name, mode string, withBgWrites bool) latencyReport {
		var stopWrites atomic.Bool
		var writesDone chan struct{}
		var writeCount atomic.Int64

		if withBgWrites {
			writesDone = make(chan struct{})
			go func() {
				defer close(writesDone)
				wIdx := 0
				for !stopWrites.Load() {
					eid := fmt.Sprintf("bg_ent_%d_%d", time.Now().UnixNano(), wIdx)
					_, err := backend.DB().ExecContext(ctx, `
						INSERT OR IGNORE INTO entities (id, namespace, name, type, summary, mention_count, first_seen_at, last_seen_at)
						VALUES (?, ?, ?, 'concept', 'Background extraction entity', 1, 1700000000, 1700000000)
					`, eid, ns, eid)
					if err == nil {
						writeCount.Add(1)
					}
					wIdx++
					time.Sleep(2 * time.Millisecond)
				}
			}()
		}

		for w := range warmupRuns {
			q := queries[w%len(queries)]
			if mode == "hybrid" {
				_, _ = backend.HybridSearchChunks(ctx, ns, q.text, q.vec, storage.HybridChunkSearchOpts{TopK: 5, Alpha: 0.5, RRFConstant: 60})
			} else {
				sc := DefaultSearchConfig()
				sc.TopK = 5
				_, _ = retriever.Search(ctx, ns, q.text, q.vec, sc)
			}
		}

		totals := make([]time.Duration, 0, numQueries)
		var sumSeed, sumExpand, sumPPR, sumHydr time.Duration
		for _, q := range queries {
			start := time.Now()
			if mode == "hybrid" {
				if _, err := backend.HybridSearchChunks(ctx, ns, q.text, q.vec, storage.HybridChunkSearchOpts{TopK: 5, Alpha: 0.5, RRFConstant: 60}); err != nil {
					t.Fatalf("%s failed: %v", name, err)
				}
				totals = append(totals, time.Since(start))
			} else {
				sc := DefaultSearchConfig()
				sc.TopK = 5
				res, err := retriever.Search(ctx, ns, q.text, q.vec, sc)
				if err != nil {
					t.Fatalf("%s failed: %v", name, err)
				}
				totals = append(totals, res.Stats.TotalDuration)
				sumSeed += res.Stats.SeedDuration
				sumExpand += res.Stats.ExpandDuration
				sumPPR += res.Stats.PPRDuration
				sumHydr += res.Stats.HydrateDuration
			}
		}

		if withBgWrites {
			stopWrites.Store(true)
			<-writesDone
			t.Logf("  [%s] completed %d concurrent background writes during queries", name, writeCount.Load())
		}

		sort.Slice(totals, func(i, j int) bool { return totals[i] < totals[j] })
		n := len(totals)
		return latencyReport{
			name:      name,
			p50:       totals[n/2],
			p95:       totals[min(n-1, int(float64(n)*0.95))],
			avgSeed:   sumSeed / time.Duration(n),
			avgExpand: sumExpand / time.Duration(n),
			avgPPR:    sumPPR / time.Duration(n),
			avgHydr:   sumHydr / time.Duration(n),
		}
	}

	reports := []latencyReport{
		runBench("Baseline Hybrid (idle)", "hybrid", false),
		runBench("GraphRAG RelFTS (idle)", "graph", false),
		runBench("GraphRAG RelFTS (+bg writes)", "graph", true),
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	for _, r := range reports {
		t.Logf("%-30s | p50=%8s | p95=%8s | seed=%8s | exp=%8s | ppr=%7s | hydr=%7s",
			r.name, r.p50.Round(time.Microsecond*100), r.p95.Round(time.Microsecond*100),
			r.avgSeed.Round(time.Microsecond*100), r.avgExpand.Round(time.Microsecond*100),
			r.avgPPR.Round(time.Microsecond*100), r.avgHydr.Round(time.Microsecond*100))
	}
	t.Logf("Go HeapAlloc: %.1f MB, Total DB on disk: %.1f MB", float64(mem.HeapAlloc)/(1024*1024), dbSizeMB)
}

func populate25KFixture(
	ctx context.Context,
	db *sql.DB,
	ns, colID string,
	dim, numChunks, numEntities, numRels, numMentions int,
) error {
	rng := rand.New(rand.NewPCG(1337, 2026))
	now := time.Now().Unix()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO collections (id, namespace, name, description, chunk_config, graph_enabled, created_at) VALUES (?, ?, 'bench', '25K benchmark', '{}', 1, ?)`,
		colID, ns, now,
	); err != nil {
		return err
	}

	numDocs := numChunks / 25
	docStmt, err := tx.PrepareContext(ctx, `INSERT INTO documents (id, namespace, collection_id, title, content, content_type, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'text', ?, ?)`)
	if err != nil {
		return err
	}
	for d := range numDocs {
		docID := fmt.Sprintf("doc_%05d", d)
		if _, err := docStmt.ExecContext(ctx, docID, ns, colID, fmt.Sprintf("Architecture Doc %05d", d), "Synthetic benchmark document", now, now); err != nil {
			return err
		}
	}
	docStmt.Close()

	centroids := make([][]float32, 100)
	for c := range 100 {
		centroids[c] = makeRandomUnitVec(rng, dim)
	}

	chunkStmt, err := tx.PrepareContext(ctx, `INSERT INTO chunks (id, document_id, namespace, collection_id, content, chunk_index, token_count) VALUES (?, ?, ?, ?, ?, ?, 64)`)
	if err != nil {
		return err
	}
	chunkEmbStmt, err := tx.PrepareContext(ctx, `INSERT INTO chunk_embeddings (chunk_id, embedding) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	for i := range numChunks {
		cid := fmt.Sprintf("chk_%05d", i)
		docID := fmt.Sprintf("doc_%05d", i/25)
		cluster := i % 100
		content := fmt.Sprintf("Service-%04d routes traffic to Datastore-%04d managed by Engineer-%04d hosted by Vendor-%04d in zone-%02d.",
			cluster*10+(i%10), cluster*10+((i+2)%10), cluster*10+((i+4)%10), cluster*10+((i+6)%10), cluster)
		if _, err := chunkStmt.ExecContext(ctx, cid, docID, ns, colID, content, i%25); err != nil {
			return err
		}
		if _, err := chunkEmbStmt.ExecContext(ctx, cid, encodeBenchFloat32LE(perturbUnitVec(rng, centroids[cluster], 0.25))); err != nil {
			return err
		}
	}
	chunkStmt.Close()
	chunkEmbStmt.Close()

	entStmt, err := tx.PrepareContext(ctx, `INSERT INTO entities (id, namespace, name, type, aliases, summary, mention_count, first_seen_at, last_seen_at) VALUES (?, ?, ?, ?, '[]', ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	aliasStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO entity_aliases (namespace, alias, entity_id) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	entEmbStmt, err := tx.PrepareContext(ctx, `INSERT INTO entity_embeddings (entity_id, embedding) VALUES (?, ?)`)
	if err != nil {
		return err
	}

	entNames := make([]string, numEntities)
	kinds := []string{"Service", "Middleware", "Datastore", "Engineer", "Vendor"}
	typesList := []string{"product", "product", "product", "person", "organization"}
	for i := range numEntities {
		eid := fmt.Sprintf("ent_%05d", i)
		cluster := (i / 10) % 100
		k := i % len(kinds)
		name := fmt.Sprintf("%s-%04d", kinds[k], i)
		entNames[i] = name
		summary := fmt.Sprintf("%s is a production %s component in domain %02d.", name, typesList[k], cluster)
		mentionCnt := 5
		if i < 50 {
			mentionCnt = 200
		}
		if _, err := entStmt.ExecContext(ctx, eid, ns, name, typesList[k], summary, mentionCnt, now, now); err != nil {
			return err
		}
		if _, err := aliasStmt.ExecContext(ctx, ns, strings.ToLower(name), eid); err != nil {
			return err
		}
		if _, err := entEmbStmt.ExecContext(ctx, eid, encodeBenchFloat32LE(perturbUnitVec(rng, centroids[cluster], 0.20))); err != nil {
			return err
		}
	}
	entStmt.Close()
	aliasStmt.Close()
	entEmbStmt.Close()

	// SQLite v4 triggers automatically populate relationships_fts on insert.
	relStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO entity_relationships (id, namespace, source_entity_id, target_entity_id, relation_type, description, confidence, mention_count, first_seen_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	relTypes := []string{"routes_to", "persists_in", "managed_by", "hosted_by", "depends_on"}
	for i := range numRels {
		rid := fmt.Sprintf("rel_%05d", i)
		srcIdx := i % numEntities
		var tgtIdx int
		if i%5 == 0 {
			tgtIdx = rng.IntN(50)
		} else {
			tgtIdx = (srcIdx + 1 + (i % 7)) % numEntities
		}
		if tgtIdx == srcIdx {
			tgtIdx = (tgtIdx + 1) % numEntities
		}
		rType := relTypes[i%len(relTypes)]
		desc := fmt.Sprintf("%s %s %s in production cluster.", entNames[srcIdx], strings.ReplaceAll(rType, "_", " "), entNames[tgtIdx])
		conf := 0.75 + 0.25*rng.Float64()
		if _, err := relStmt.ExecContext(ctx, rid, ns, fmt.Sprintf("ent_%05d", srcIdx), fmt.Sprintf("ent_%05d", tgtIdx), rType, desc, conf, 1+(i%5), now, now); err != nil {
			return err
		}
	}
	relStmt.Close()

	menStmt, err := tx.PrepareContext(ctx, `INSERT INTO entity_mentions (id, entity_id, namespace, source_type, source_id, context, created_at) VALUES (?, ?, ?, 'knowledge', ?, '', ?)`)
	if err != nil {
		return err
	}
	for i := range numMentions {
		chunkIdx := i % numChunks
		cluster := chunkIdx % 100
		baseEnt := (cluster * 10) % numEntities
		entIdx := (baseEnt + (i/numChunks)*2 + (chunkIdx % 10)) % numEntities
		if _, err := menStmt.ExecContext(ctx, fmt.Sprintf("men_%06d", i), fmt.Sprintf("ent_%05d", entIdx), ns, fmt.Sprintf("chk_%05d", chunkIdx), now-int64(i%10000)); err != nil {
			return err
		}
	}
	menStmt.Close()

	return tx.Commit()
}

func encodeBenchFloat32LE(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func makeRandomUnitVec(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	sum := 0.0
	for i := range dim {
		x := rng.NormFloat64()
		v[i] = float32(x)
		sum += x * x
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range dim {
		v[i] *= inv
	}
	return v
}

func perturbUnitVec(rng *rand.Rand, base []float32, noise float64) []float32 {
	v := make([]float32, len(base))
	sum := 0.0
	for i, b := range base {
		x := float64(b) + noise*rng.NormFloat64()
		v[i] = float32(x)
		sum += x * x
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}

func makeClusterVector(rng *rand.Rand, cluster, dim int, noise float64) []float32 {
	crng := rand.New(rand.NewPCG(1337, 2026))
	var centroid []float32
	for c := 0; c <= cluster; c++ {
		centroid = makeRandomUnitVec(crng, dim)
	}
	return perturbUnitVec(rng, centroid, noise)
}

func fileSizeMB(path string) float64 {
	total := int64(0)
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if st, err := os.Stat(p); err == nil {
			total += st.Size()
		}
	}
	return float64(total) / (1024 * 1024)
}
