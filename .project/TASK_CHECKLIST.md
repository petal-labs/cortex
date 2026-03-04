# Cortex Task Checklist

Quick reference for tracking implementation progress. See IMPLEMENTATION_PLAN.md for details.

---

## Phase 1: Foundation

### 1.1 Project Setup & Go Module
- [ ] 1.1.1 Initialize Go Module
- [ ] 1.1.2 Create Directory Structure
- [ ] 1.1.3 Add Core Dependencies
- [ ] 1.1.4 Create Configuration System
- [ ] 1.1.5 Create Shared Type Definitions

### 1.2 Storage Interface & SQLite Backend
- [ ] 1.2.1 Define Storage Backend Interface
- [ ] 1.2.2 Create SQLite Backend Scaffold
- [ ] 1.2.3 Implement SQLite Schema Migrations
- [ ] 1.2.4 Implement SQLite Conversation Storage
- [ ] 1.2.5 Implement SQLite Knowledge Storage
- [ ] 1.2.6 Implement SQLite Context Storage
- [ ] 1.2.7 Implement SQLite Entity Storage
- [ ] 1.2.8 Integrate vec0 Extension for Vector Search

### 1.3 Iris Embedding Integration
- [ ] 1.3.1 Create Embedding Provider Interface
- [ ] 1.3.2 Implement Iris Embedding Client
- [ ] 1.3.3 Add Embedding Cache

### 1.4 Conversation Memory Engine
- [ ] 1.4.1 Create Conversation Engine
- [ ] 1.4.2 Implement Append Operation
- [ ] 1.4.3 Implement History Operation
- [ ] 1.4.4 Implement Search Operation
- [ ] 1.4.5 Implement Clear Operation
- [ ] 1.4.6 Implement Threads List Operation

### 1.5 Knowledge Store Engine
- [ ] 1.5.1 Create Knowledge Engine
- [ ] 1.5.2 Implement Fixed Chunking Strategy
- [ ] 1.5.3 Implement Sentence Chunking Strategy
- [ ] 1.5.4 Implement Paragraph Chunking Strategy
- [ ] 1.5.5 Implement Ingest Operation
- [ ] 1.5.6 Implement Search Operation
- [ ] 1.5.7 Implement Get and Delete Operations
- [ ] 1.5.8 Implement Collections Management

### 1.6 Workflow Context Engine
- [ ] 1.6.1 Create Context Engine
- [ ] 1.6.2 Implement Get and Set Operations
- [ ] 1.6.3 Implement Deep Merge Strategy
- [ ] 1.6.4 Implement Other Merge Strategies
- [ ] 1.6.5 Implement Merge Operation
- [ ] 1.6.6 Implement List and Delete Operations
- [ ] 1.6.7 Implement History Operation

### 1.7 Entity Memory Engine (Core)
- [ ] 1.7.1 Create Entity Engine
- [ ] 1.7.2 Implement Query Operation
- [ ] 1.7.3 Implement Search Operation
- [ ] 1.7.4 Implement Relationships Operation
- [ ] 1.7.5 Implement Update Operation
- [ ] 1.7.6 Implement Merge Operation
- [ ] 1.7.7 Implement List and Delete Operations

### 1.8 MCP Server Implementation
- [ ] 1.8.1 Create MCP Server Scaffold
- [ ] 1.8.2 Implement tools/list Handler
- [ ] 1.8.3 Implement Conversation Tool Handlers
- [ ] 1.8.4 Implement Knowledge Tool Handlers
- [ ] 1.8.5 Implement Context Tool Handlers
- [ ] 1.8.6 Implement Entity Tool Handlers
- [ ] 1.8.7 Implement Namespace Enforcement

### 1.9 CLI Implementation (Basic)
- [ ] 1.9.1 Create CLI Framework
- [ ] 1.9.2 Implement Serve Command
- [ ] 1.9.3 Implement Config Command

### Phase 1 Verification
- [ ] V1.1 End-to-End Conversation Test
- [ ] V1.2 End-to-End Knowledge Test
- [ ] V1.3 End-to-End Context Test
- [ ] V1.4 End-to-End Entity Test
- [ ] V1.5 MCP Protocol Compliance Test

---

## Phase 2: Advanced Features

### 2.1 Conversation Summarization
- [ ] 2.1.1 Create Summarization Client
- [ ] 2.1.2 Implement Summarize Operation
- [ ] 2.1.3 Add Auto-Summarization Trigger
- [ ] 2.1.4 Add MCP Handler

### 2.2 Semantic Chunking
- [ ] 2.2.1 Implement Semantic Chunker
- [ ] 2.2.2 Integrate with Knowledge Engine

### 2.3 Entity Extraction Pipeline
- [ ] 2.3.1 Create Entity Extractor
- [ ] 2.3.2 Create Name Resolver
- [ ] 2.3.3 Create Queue Processor
- [ ] 2.3.4 Add Extraction Hooks
- [ ] 2.3.5 Implement Extraction Modes
- [ ] 2.3.6 Implement Entity Summary Regeneration

### 2.4 Context Version History
- [ ] 2.4.1 Implement Version Conflict Detection
- [ ] 2.4.2 Implement History Retrieval

### 2.5 CLI Management Commands
- [ ] 2.5.1 Knowledge CLI Commands
- [ ] 2.5.2 Conversation CLI Commands
- [ ] 2.5.3 Context CLI Commands
- [ ] 2.5.4 Entity CLI Commands
- [ ] 2.5.5 Namespace CLI Commands

### Phase 2 Verification
- [ ] V2.1 Summarization Test
- [ ] V2.2 Entity Extraction Test
- [ ] V2.3 Context Concurrency Test

---

## Phase 3: Production Hardening

### 3.1 PostgreSQL + pgvector Backend
- [ ] 3.1.1 Create pgvector Backend Scaffold
- [ ] 3.1.2 Implement pgvector Schema Migrations
- [ ] 3.1.3 Implement All Storage Operations
- [ ] 3.1.4 Add Backend Selection

### 3.2 SSE Transport
- [ ] 3.2.1 Implement SSE Server
- [ ] 3.2.2 Add Transport Selection

### 3.3 Observability
- [ ] 3.3.1 Add Prometheus Metrics
- [ ] 3.3.2 Implement Structured Logging

### 3.4 Retention & Garbage Collection
- [ ] 3.4.1 Implement Background GC Worker
- [ ] 3.4.2 Add CLI GC Command

### 3.5 Backup & Restore
- [ ] 3.5.1 Implement SQLite Backup
- [ ] 3.5.2 Implement Export/Import

### Phase 3 Verification
- [ ] V3.1 pgvector Test
- [ ] V3.2 SSE Transport Test
- [ ] V3.3 Metrics Test
- [ ] V3.4 GC Test

---

## Phase 4: Ecosystem Integration

### 4.1 Hybrid Search
- [ ] 4.1.1 Add Full-Text Search Indexes
- [ ] 4.1.2 Implement Reciprocal Rank Fusion

### 4.2 Bulk Ingest Optimization
- [ ] 4.2.1 Implement Batch Processing
- [ ] 4.2.2 Add Progress Reporting

### 4.3 HTTP API Mode
- [ ] 4.3.1 Implement HTTP Server

---

## Summary

| Phase | Tasks | Status |
|-------|-------|--------|
| Phase 1 | 45 tasks + 5 verification | Not Started |
| Phase 2 | 18 tasks + 3 verification | Not Started |
| Phase 3 | 12 tasks + 4 verification | Not Started |
| Phase 4 | 5 tasks | Not Started |
| **Total** | **88 tasks** | **Not Started** |
