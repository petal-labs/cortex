package graphrag

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/petal-labs/cortex/v2/internal/config"
	"github.com/petal-labs/cortex/v2/internal/storage"
	"github.com/petal-labs/cortex/v2/internal/storage/sqlite"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

func evalTokenize(s string) []string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

var evalStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "in": true, "on": true,
	"at": true, "to": true, "for": true, "of": true, "with": true, "by": true,
	"from": true, "as": true, "and": true, "or": true, "not": true, "that": true,
	"this": true, "these": true, "those": true, "it": true, "its": true,
	"who": true, "what": true, "which": true, "where": true, "when": true,
	"why": true, "how": true, "does": true, "do": true, "did": true, "has": true,
	"have": true, "had": true, "can": true, "could": true, "would": true,
	"should": true, "will": true, "about": true, "into": true, "between": true,
}

// deterministicEmbed produces a realistic, deterministic unit-length embedding
// combining lexical token hashes, character 3-gram hashes, and shared semantic
// concept axes. This keeps the offline CI eval suite 100% reproducible with
// zero external API keys while faithfully capturing lexical + semantic overlap.
func deterministicEmbed(text string, dim int) []float32 {
	vec := make([]float64, dim)
	tokens := evalTokenize(text)

	addFeature := func(key string, weight float64) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(key))
		sum := h.Sum64()
		idx1 := int(sum % uint64(dim))
		idx2 := int((sum >> 17) % uint64(dim))
		sign1 := 1.0
		if (sum>>33)&1 == 1 {
			sign1 = -1.0
		}
		sign2 := 1.0
		if (sum>>49)&1 == 1 {
			sign2 = -1.0
		}
		vec[idx1] += sign1 * weight
		vec[idx2] += sign2 * (weight * 0.5)
	}

	for _, tok := range tokens {
		if len(tok) < 2 || evalStopWords[tok] {
			continue
		}
		// Exact token feature
		addFeature("tok:"+tok, 1.0)
		// Character 3-grams for morphological similarity (e.g., manage/manages/manager)
		if len(tok) >= 4 {
			for i := 0; i+3 <= len(tok); i++ {
				addFeature("tri:"+tok[i:i+3], 0.25)
			}
		}
		// Shared semantic concept axes (captures real-world embedding paraphrases)
		if concept, ok := evalConceptMap[tok]; ok {
			addFeature("concept:"+concept, 1.6)
		}
	}

	// Normalize to unit L2 length.
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	out := make([]float32, dim)
	if norm == 0 {
		out[0] = 1.0
		return out
	}
	inv := 1.0 / math.Sqrt(norm)
	for i, v := range vec {
		out[i] = float32(v * inv)
	}
	return out
}

var evalConceptMap = map[string]string{
	// Storage / database synonyms
	"database": "datastore", "datastore": "datastore", "db": "datastore",
	"storage": "datastore", "engine": "datastore", "persists": "datastore",
	"stores": "datastore", "sql": "datastore", "kv": "datastore",
	// Ownership / management synonyms
	"manages": "ownership", "manager": "ownership", "owns": "ownership",
	"owner": "ownership", "oversees": "ownership", "maintains": "ownership",
	"maintainer": "ownership", "lead": "ownership", "oncall": "ownership",
	"on-call": "ownership", "steward": "ownership", "responsible": "ownership",
	// Dependency synonyms
	"uses": "dependency", "relies": "dependency", "depends": "dependency",
	"backed": "dependency", "powered": "dependency", "calls": "dependency",
	"routes": "dependency", "consumes": "dependency",
	// Incident / outage synonyms
	"incident": "outage", "outage": "outage", "downtime": "outage",
	"failure": "outage", "disruption": "outage", "postmortem": "outage",
	"sev1": "outage", "alert": "outage",
	// Vendor / provider synonyms
	"vendor": "supplier", "provider": "supplier", "supplier": "supplier",
	"contractor": "supplier", "hosted": "supplier",
	// Compliance / audit synonyms
	"compliance": "audit", "soc2": "audit", "hipaa": "audit",
	"gdpr": "audit", "audit": "audit", "certification": "audit",
	"encrypted": "security", "encryption": "security", "kms": "security",
	"key": "security", "keys": "security", "rotation": "security",
	// Infrastructure / deployment synonyms
	"kubernetes": "k8s", "k8s": "k8s", "cluster": "k8s",
	"region": "datacenter", "datacenter": "datacenter", "zone": "datacenter",
	"failover": "resilience", "replica": "resilience", "backup": "resilience",
	"runbook": "ops", "playbook": "ops", "remediation": "ops",
}

type evalEntitySpec struct {
	id      string
	name    string
	entType types.EntityType
	aliases []string
	summary string
}

type evalRelSpec struct {
	id           string
	sourceID     string
	targetID     string
	relationType string
	description  string
	confidence   float64
}

type evalChunkSpec struct {
	id        string
	docTitle  string
	content   string
	entityIDs []string
}

type evalQuestion struct {
	id            string
	category      string // "single_hop", "two_hop", "three_hop"
	query         string
	answerChunkID string   // The primary target chunk containing the answer
	supportChunks []string // All supporting chunks in the reasoning chain
}

// buildEvalCorpus constructs a realistic enterprise engineering corpus with
// 10 distinct multi-hop domains (each with 3-4 chained chunks + distractors)
// yielding 30 multi-hop questions (20 two-hop + 10 three-hop) and 10 single-hop
// regression questions.
func buildEvalCorpus() ([]evalEntitySpec, []evalRelSpec, []evalChunkSpec, []evalQuestion) {
	type domainDef struct {
		prefix       string
		service      string
		serviceAlias string
		middleware   string
		datastore    string
		datastoreAl  string
		owner        string
		vendor       string
		region       string
		policy       string
		incidentCode string
		runbookCode  string
	}

	domains := []domainDef{
		{"d01", "Checkout Gateway", "cgw", "Order Ledger", "RaftStore", "rstore", "Priya Nair", "Helios Cloud", "us-east-alpha", "PCI-Vault-9", "INC-401", "RB-101"},
		{"d02", "Billing Engine", "billeng", "Invoice Bus", "CinderDB", "cdb", "Marcus Vance", "Stratos Hosting", "eu-west-bravo", "SOX-Ledger-4", "INC-402", "RB-102"},
		{"d03", "Identity Broker", "authn", "Token Mesh", "KeyVaultSQL", "kvsql", "Elena Rostova", "Aegis Corp", "us-west-charlie", "SOC2-Auth-7", "INC-403", "RB-103"},
		{"d04", "Telemetry Collector", "telcol", "Span Router", "ChronoStore", "cstore", "Devon Brooks", "PulseNet", "ap-south-delta", "ISO-Log-2", "INC-404", "RB-104"},
		{"d05", "Fraud Scorer", "fscore", "Feature Pipe", "VectorVault", "vvault", "Kenji Sato", "Nimbus AI", "eu-central-echo", "AML-Rule-8", "INC-405", "RB-105"},
		{"d06", "Search Frontend", "sfront", "Query Planner", "IndexCore", "icore", "Hannah Lindqvist", "Boreal Compute", "us-east-foxtrot", "GDPR-Idx-3", "INC-406", "RB-106"},
		{"d07", "Notification Hub", "nhub", "Dispatch Queue", "MailSpoolDB", "mspool", "Tomas Herrera", "RelayWorks", "us-central-golf", "CANSPAM-5", "INC-407", "RB-107"},
		{"d08", "Catalog API", "catapi", "Inventory Sync", "ShelfDB", "sdb", "Amara Okafor", "Meridian Infra", "eu-north-hotel", "SKU-Audit-6", "INC-408", "RB-108"},
		{"d09", "Payroll Scheduler", "paysched", "Disbursement Worker", "TreasuryDB", "tdb", "Liam Gallagher", "Vertex FinOps", "us-west-india", "FIN-Ctrl-1", "INC-409", "RB-109"},
		{"d10", "Document Signer", "docsign", "Envelope Service", "SealArchive", "sarch", "Sofia Chen", "Ironclad HSM", "ca-east-juliet", "EIDAS-Cert-9", "INC-410", "RB-110"},
	}

	var entities []evalEntitySpec
	var rels []evalRelSpec
	var chunks []evalChunkSpec
	var questions []evalQuestion

	for _, d := range domains {
		p := d.prefix
		eSvc := p + "_svc"
		eMid := p + "_mid"
		eDB := p + "_db"
		eOwn := p + "_own"
		eVen := p + "_ven"
		eReg := p + "_reg"
		ePol := p + "_pol"
		eInc := p + "_inc"

		entities = append(entities,
			evalEntitySpec{eSvc, d.service, types.EntityTypeProduct, []string{d.serviceAlias}, fmt.Sprintf("%s is a customer-facing microservice.", d.service)},
			evalEntitySpec{eMid, d.middleware, types.EntityTypeProduct, nil, fmt.Sprintf("%s is an internal processing component.", d.middleware)},
			evalEntitySpec{eDB, d.datastore, types.EntityTypeProduct, []string{d.datastoreAl}, fmt.Sprintf("%s is a persistent transactional datastore.", d.datastore)},
			evalEntitySpec{eOwn, d.owner, types.EntityTypePerson, nil, fmt.Sprintf("%s is an engineering lead and on-call steward.", d.owner)},
			evalEntitySpec{eVen, d.vendor, types.EntityTypeOrganization, nil, fmt.Sprintf("%s is an external infrastructure supplier.", d.vendor)},
			evalEntitySpec{eReg, d.region, types.EntityTypeLocation, nil, fmt.Sprintf("%s is a production cloud datacenter zone.", d.region)},
			evalEntitySpec{ePol, d.policy, types.EntityTypeConcept, nil, fmt.Sprintf("%s is a security and audit compliance standard.", d.policy)},
			evalEntitySpec{eInc, d.incidentCode, types.EntityTypeEvent, nil, fmt.Sprintf("%s was a production availability disruption.", d.incidentCode)},
		)

		rels = append(rels,
			evalRelSpec{p + "_r1", eSvc, eMid, "routes_to", fmt.Sprintf("%s routes workload traffic through %s.", d.service, d.middleware), 0.95},
			evalRelSpec{p + "_r2", eMid, eDB, "persists_in", fmt.Sprintf("%s persists durable state inside %s.", d.middleware, d.datastore), 0.95},
			evalRelSpec{p + "_r3", eOwn, eDB, "maintains", fmt.Sprintf("%s is the primary on-call maintainer for %s.", d.owner, d.datastore), 0.95},
			evalRelSpec{p + "_r4", eDB, eVen, "hosted_by", fmt.Sprintf("%s cluster nodes are hosted by supplier %s.", d.datastore, d.vendor), 0.92},
			evalRelSpec{p + "_r5", eVen, eReg, "operates_in", fmt.Sprintf("%s operates dedicated racks inside zone %s.", d.vendor, d.region), 0.90},
			evalRelSpec{p + "_r6", eDB, ePol, "governed_by", fmt.Sprintf("%s encryption and key rotation are governed by %s.", d.datastore, d.policy), 0.93},
			evalRelSpec{p + "_r7", eInc, eMid, "triggered_by", fmt.Sprintf("Disruption %s originated from a queue backlog in %s.", d.incidentCode, d.middleware), 0.94},
		)

		// Chain chunks (notice each chunk only mentions its local hop — no shortcut keywords!).
		c1 := p + "_c1"  // Service -> Middleware
		c2 := p + "_c2"  // Middleware -> Datastore
		c3 := p + "_c3"  // Owner -> Datastore + Runbook
		c4 := p + "_c4"  // Datastore -> Vendor + Compliance Policy
		c5 := p + "_c5"  // Vendor -> Region
		c6 := p + "_c6"  // Incident -> Middleware
		cD1 := p + "_d1" // Distractor: Service UI / metrics (shares Service name + "manager"/"latency" words)
		cD2 := p + "_d2" // Distractor: General team roster note (shares "on-call"/"database" words)

		chunks = append(chunks,
			evalChunkSpec{
				id:        c1,
				docTitle:  d.service + " Architecture",
				content:   fmt.Sprintf("%s (%s) handles inbound API requests and forwards state transitions to %s over gRPC.", d.service, d.serviceAlias, d.middleware),
				entityIDs: []string{eSvc, eMid},
			},
			evalChunkSpec{
				id:        c2,
				docTitle:  d.middleware + " Persistence",
				content:   fmt.Sprintf("%s commits durable checkpoints to %s (%s) using write-ahead journaling.", d.middleware, d.datastore, d.datastoreAl),
				entityIDs: []string{eMid, eDB},
			},
			evalChunkSpec{
				id:        c3,
				docTitle:  d.datastore + " Ownership",
				content:   fmt.Sprintf("%s serves as the on-call lead responsible for %s capacity planning and executes %s during failover drills.", d.owner, d.datastore, d.runbookCode),
				entityIDs: []string{eOwn, eDB},
			},
			evalChunkSpec{
				id:        c4,
				docTitle:  d.datastore + " Hosting & Audit",
				content:   fmt.Sprintf("Production clusters for %s are provisioned through %s and enforce quarterly key rotation under %s.", d.datastore, d.vendor, d.policy),
				entityIDs: []string{eDB, eVen, ePol},
			},
			evalChunkSpec{
				id:        c5,
				docTitle:  d.vendor + " Datacenters",
				content:   fmt.Sprintf("%s runs isolated bare-metal availability pods located in %s with dual fiber uplinks.", d.vendor, d.region),
				entityIDs: []string{eVen, eReg},
			},
			evalChunkSpec{
				id:        c6,
				docTitle:  d.incidentCode + " Postmortem",
				content:   fmt.Sprintf("Postmortem %s traced elevated error rates to a thread starvation defect inside %s during peak traffic.", d.incidentCode, d.middleware),
				entityIDs: []string{eInc, eMid},
			},
			// Distractors that lexically overlap with multi-hop query words to make retrieval genuinely challenging.
			evalChunkSpec{
				id:        cD1,
				docTitle:  d.service + " Dashboard Notes",
				content:   fmt.Sprintf("%s dashboard tracks p99 latency, SLA alerts, region banner flags, and product manager feedback notes.", d.service),
				entityIDs: []string{eSvc},
			},
			evalChunkSpec{
				id:        cD2,
				docTitle:  "General Ops Policy " + p,
				content:   fmt.Sprintf("Standard engineering managers review compliance audit checklists, vendor invoices, and storage alerts every Monday for %s.", d.service),
				entityIDs: []string{eSvc},
			},
		)

		// 1 Single-hop regression question per domain (10 total)
		questions = append(questions, evalQuestion{
			id:            p + "_q_1hop",
			category:      "single_hop",
			query:         fmt.Sprintf("What runbook does %s execute for %s failover drills?", d.owner, d.datastore),
			answerChunkID: c3,
			supportChunks: []string{c3},
		})

		// 2 Two-hop questions per domain (20 total)
		// Two-hop A: Service -> Middleware -> Datastore (asks about datastore via service)
		questions = append(questions, evalQuestion{
			id:            p + "_q_2hop_a",
			category:      "two_hop",
			query:         fmt.Sprintf("Which durable storage system backs %s?", d.service),
			answerChunkID: c2,
			supportChunks: []string{c1, c2},
		})
		// Two-hop B: Incident -> Middleware -> Datastore (asks about datastore or owner via incident)
		// Or Datastore -> Vendor -> Region (asks which datacenter zone hosts Datastore)
		questions = append(questions, evalQuestion{
			id:            p + "_q_2hop_b",
			category:      "two_hop",
			query:         fmt.Sprintf("In which datacenter zone are %s clusters physically located?", d.datastore),
			answerChunkID: c5,
			supportChunks: []string{c4, c5},
		})

		// 1 Three-hop question per domain (10 total)
		// Service -> Middleware -> Datastore -> Owner (3 hops across entities!)
		// Note: c1 has Service+Middleware, c2 has Middleware+Datastore, c3 has Owner+Datastore.
		questions = append(questions, evalQuestion{
			id:            p + "_q_3hop",
			category:      "three_hop",
			query:         fmt.Sprintf("Who is the on-call lead responsible for the datastore behind %s?", d.service),
			answerChunkID: c3,
			supportChunks: []string{c1, c2, c3},
		})
	}

	return entities, rels, chunks, questions
}

type evalMetrics struct {
	count         int
	answerRecall5 float64
	chainRecall5  float64
	mrr5          float64
	ndcg5         float64
}

func computeMetrics(results []*types.ChunkResult, q evalQuestion) (ansHit, chainRec, rr, ndcg float64) {
	topK := min(len(results), 5)
	supportSet := make(map[string]bool, len(q.supportChunks))
	for _, id := range q.supportChunks {
		supportSet[id] = true
	}

	supportHits := 0
	dcg := 0.0
	for i := range topK {
		cid := results[i].Chunk.ID
		if cid == q.answerChunkID {
			ansHit = 1.0
			if rr == 0 {
				rr = 1.0 / float64(i+1)
			}
		}
		if supportSet[cid] {
			supportHits++
			rel := 1.0
			if cid == q.answerChunkID {
				rel = 2.0
			}
			dcg += (math.Pow(2, rel) - 1) / math.Log2(float64(i+2))
		}
	}

	// Ideal DCG: answer chunk (rel=2 -> gain 2^2-1 = 3) at rank 1,
	// remaining support chunks (rel=1 -> gain 2^1-1 = 1) next.
	idcg := 3.0 / math.Log2(2)
	extra := min(len(q.supportChunks)-1, 4)
	for i := 1; i <= extra; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}
	if idcg > 0 {
		ndcg = dcg / idcg
	}
	if len(q.supportChunks) > 0 {
		chainRec = float64(supportHits) / float64(len(q.supportChunks))
	}
	return ansHit, chainRec, rr, ndcg
}

func TestGate1OfflineMultiHopEval(t *testing.T) {
	const (
		ns  = "eval-multihop"
		dim = 384
	)

	cfg := config.DefaultConfig()
	cfg.Storage.Backend = "sqlite"
	cfg.Storage.DataDir = t.TempDir()
	cfg.Embedding.Dimensions = dim

	backend, err := sqlite.New(cfg)
	if err != nil {
		t.Fatalf("sqlite.New failed: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()

	// Create collection.
	col := &types.Collection{
		ID:           "col-eval",
		Namespace:    ns,
		Name:         "engineering-docs",
		ChunkConfig:  types.DefaultChunkConfig(),
		GraphEnabled: true,
		CreatedAt:    time.Now().UTC(),
	}
	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("CreateCollection failed: %v", err)
	}

	entities, rels, chunks, questions := buildEvalCorpus()
	now := time.Now().UTC()

	// Insert entities & embeddings.
	entityMentionCounts := make(map[string]int64, len(entities))
	for _, ch := range chunks {
		for _, eid := range ch.entityIDs {
			entityMentionCounts[eid]++
		}
	}

	for _, e := range entities {
		ent := &types.Entity{
			ID:           e.id,
			Namespace:    ns,
			Name:         e.name,
			Type:         e.entType,
			Aliases:      e.aliases,
			Summary:      e.summary,
			MentionCount: entityMentionCounts[e.id],
			FirstSeenAt:  now,
			LastSeenAt:   now,
		}
		if err := backend.UpsertEntity(ctx, ent); err != nil {
			t.Fatalf("UpsertEntity %s: %v", e.id, err)
		}
		embText := e.name + " " + strings.Join(e.aliases, " ") + " " + e.summary
		if err := backend.StoreEntityEmbedding(ctx, e.id, deterministicEmbed(embText, dim)); err != nil {
			t.Fatalf("StoreEntityEmbedding %s: %v", e.id, err)
		}
	}

	// Insert relationships — SQLite v4 triggers populate relationships_fts automatically.
	for _, r := range rels {
		rel := &types.EntityRelationship{
			ID:             r.id,
			Namespace:      ns,
			SourceEntityID: r.sourceID,
			TargetEntityID: r.targetID,
			RelationType:   r.relationType,
			Description:    r.description,
			Confidence:     r.confidence,
			MentionCount:   1,
			FirstSeenAt:    now,
			LastSeenAt:     now,
		}
		if err := backend.UpsertRelationship(ctx, rel); err != nil {
			t.Fatalf("UpsertRelationship %s: %v", r.id, err)
		}
	}

	// Insert documents, chunks, embeddings, and entity_mentions.
	for i, ch := range chunks {
		docID := "doc_" + ch.id
		if err := backend.InsertDocument(ctx, &types.Document{
			ID:           docID,
			Namespace:    ns,
			CollectionID: col.ID,
			Title:        ch.docTitle,
			Content:      ch.content,
			ContentType:  "text",
			CreatedAt:    now,
			UpdatedAt:    now,
		}); err != nil {
			t.Fatalf("InsertDocument %s: %v", docID, err)
		}
		if err := backend.InsertChunks(ctx, []*types.Chunk{{
			ID:           ch.id,
			DocumentID:   docID,
			Namespace:    ns,
			CollectionID: col.ID,
			Content:      ch.content,
			Embedding:    deterministicEmbed(ch.docTitle+" "+ch.content, dim),
			Index:        i,
			TokenCount:   len(strings.Fields(ch.content)),
		}}); err != nil {
			t.Fatalf("InsertChunks %s: %v", ch.id, err)
		}
		for j, eid := range ch.entityIDs {
			if err := backend.InsertMention(ctx, &types.EntityMention{
				ID:         fmt.Sprintf("m_%s_%d", ch.id, j),
				EntityID:   eid,
				Namespace:  ns,
				SourceType: "knowledge",
				SourceID:   ch.id,
				Context:    ch.content,
				CreatedAt:  now,
			}); err != nil {
				t.Fatalf("InsertMention %s->%s: %v", eid, ch.id, err)
			}
		}
	}

	retriever := NewRetriever(backend)

	type modeRunner struct {
		name string
		run  func(q evalQuestion, qEmb []float32) ([]*types.ChunkResult, *types.GraphContext, error)
	}

	runners := []modeRunner{
		{
			name: "Baseline Hybrid",
			run: func(q evalQuestion, qEmb []float32) ([]*types.ChunkResult, *types.GraphContext, error) {
				res, err := backend.HybridSearchChunks(ctx, ns, q.query, qEmb, storage.HybridChunkSearchOpts{
					TopK:        5,
					Alpha:       0.5,
					RRFConstant: 60,
				})
				return res, nil, err
			},
		},
		{
			name: "GraphRAG (RelFTS)",
			run: func(q evalQuestion, qEmb []float32) ([]*types.ChunkResult, *types.GraphContext, error) {
				sc := DefaultSearchConfig()
				sc.TopK = 5
				sc.RelationshipMode = RelationshipModeFTS
				res, err := retriever.Search(ctx, ns, q.query, qEmb, sc)
				if err != nil {
					return nil, nil, err
				}
				return res.Results, res.GraphContext, nil
			},
		},
		{
			name: "Ablation (NoRelSeed)",
			run: func(q evalQuestion, qEmb []float32) ([]*types.ChunkResult, *types.GraphContext, error) {
				sc := DefaultSearchConfig()
				sc.TopK = 5
				sc.RelationshipMode = RelationshipModeNone
				res, err := retriever.Search(ctx, ns, q.query, qEmb, sc)
				if err != nil {
					return nil, nil, err
				}
				return res.Results, res.GraphContext, nil
			},
		},
	}

	// Collect metrics per runner and per category.
	resultsByMode := make(map[string]map[string]*evalMetrics)
	for _, r := range runners {
		resultsByMode[r.name] = map[string]*evalMetrics{
			"single_hop": {},
			"two_hop":    {},
			"three_hop":  {},
			"multi_hop":  {},
		}
		for _, q := range questions {
			qEmb := deterministicEmbed(q.query, dim)
			hits, gctx, err := r.run(q, qEmb)
			if err != nil {
				t.Fatalf("%s failed on %s: %v", r.name, q.id, err)
			}
			if r.name != "Baseline Hybrid" && (gctx == nil || len(gctx.Entities) == 0) {
				t.Fatalf("%s returned empty graph_context on %s", r.name, q.id)
			}

			ansHit, chainRec, rr, ndcg := computeMetrics(hits, q)
			add := func(cat string) {
				m := resultsByMode[r.name][cat]
				m.count++
				m.answerRecall5 += ansHit
				m.chainRecall5 += chainRec
				m.mrr5 += rr
				m.ndcg5 += ndcg
			}
			add(q.category)
			if q.category == "two_hop" || q.category == "three_hop" {
				add("multi_hop")
			}
		}
	}

	t.Logf("\n=== Offline Multi-Hop Eval (%d questions: 10 single-hop, 20 two-hop, 10 three-hop) ===", len(questions))
	t.Logf("%-22s | %-10s | %-11s | %-10s | %-7s | %-7s", "Mode", "Category", "AnsRecall@5", "ChainRec@5", "MRR@5", "NDCG@5")
	t.Logf("%s", strings.Repeat("-", 82))
	for _, cat := range []string{"single_hop", "two_hop", "three_hop", "multi_hop"} {
		for _, r := range runners {
			m := resultsByMode[r.name][cat]
			n := float64(m.count)
			t.Logf("%-22s | %-10s | %10.1f%% | %9.1f%% | %7.3f | %7.3f",
				r.name, cat,
				100*m.answerRecall5/n,
				100*m.chainRecall5/n,
				m.mrr5/n,
				m.ndcg5/n,
			)
		}
		t.Logf("%s", strings.Repeat("-", 82))
	}

	// Enforce Go/No-Go quality criteria on production Variant B (RelFTS):
	//   1. Multi-hop Answer Recall@5 lift >= +15% absolute over Baseline Hybrid
	//   2. Single-hop Answer Recall@5 regression <= 1%
	baseMulti := resultsByMode["Baseline Hybrid"]["multi_hop"]
	baseSingle := resultsByMode["Baseline Hybrid"]["single_hop"]
	baseMultiRec := baseMulti.answerRecall5 / float64(baseMulti.count)
	baseSingleRec := baseSingle.answerRecall5 / float64(baseSingle.count)

	vMulti := resultsByMode["GraphRAG (RelFTS)"]["multi_hop"]
	vSingle := resultsByMode["GraphRAG (RelFTS)"]["single_hop"]
	vMultiRec := vMulti.answerRecall5 / float64(vMulti.count)
	vSingleRec := vSingle.answerRecall5 / float64(vSingle.count)

	lift := vMultiRec - baseMultiRec
	regression := baseSingleRec - vSingleRec
	t.Logf("GraphRAG (RelFTS) -> Multi-hop Recall@5 lift: %+.1f%% (base %.1f%% -> %.1f%%), Single-hop regression: %.1f%%",
		100*lift, 100*baseMultiRec, 100*vMultiRec, 100*regression)

	if lift < 0.15 {
		t.Errorf("GraphRAG (RelFTS) multi-hop Recall@5 lift = %.1f%%, want >= +15.0%%", 100*lift)
	}
	if regression > 0.01 {
		t.Errorf("GraphRAG (RelFTS) single-hop Recall@5 regression = %.1f%%, want <= 1.0%%", 100*regression)
	}
}
