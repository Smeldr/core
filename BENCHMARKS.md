# Smeldr — Benchmark Results

This file is the authoritative record of Smeldr's hot-path performance.

**Maintenance rule:** Run the full benchmark suite and append a new changelog
entry every time a milestone ships or a performance-relevant change is made.
Do not edit past entries — append only.

## How to run

```powershell
go test -run "^$" -bench "^Benchmark" -benchtime=3s -benchmem . 2>&1
```

---

## Benchmark catalogue

All benchmarks live in `*_test.go` files in the root `smeldr` package.

| Benchmark | Milestone | File | What it measures |
|-----------|-----------|------|-----------------|
| `BenchmarkValidateStructCached` | M1 | `node_test.go` | Struct-tag validation on a cached type (zero alloc steady state) |
| `BenchmarkQueryScanCached` | M1/M7 | `storage_test.go` | SQL column→field scan with warmed reflection cache |
| `BenchmarkSignToken` | M1 | `benchmarks_test.go` | HMAC token signing — cost per login / session refresh |
| `BenchmarkBearerHMAC_verify` | M1 | `benchmarks_test.go` | HMAC token verify — cost on **every** authenticated request |
| `BenchmarkExcerpt` | M3 | `head_test.go` | Plain-text excerpt extraction from markdown body |
| `BenchmarkSchemaFor_Article` | M3 | `schema_test.go` | JSON-LD structured data serialisation for an Article node |
| `BenchmarkWriteSitemapFragment` | M3 | `sitemap_test.go` | XML sitemap fragment generation for 20 URLs |
| **`BenchmarkHTMLTemplateRender`** | M4 | `benchmarks_test.go` | **Template render time** — full show-page pipeline: content-type negotiation → repo lookup → `TemplateData[T]` assembly → `html/template` execution |
| `BenchmarkForgeMarkdown` | M4 | `templatehelpers_test.go` | Markdown→HTML rendering via `smeldr_markdown` template helper |
| **`BenchmarkModuleRequest`** | M4 | `module_test.go` | **Request throughput (cached)** — module show-handler with warm LRU cache; measures cache-hit path cost |
| **`BenchmarkApp_Handler`** | M2/M4 | `forge_test.go` | **Request throughput (full stack)** — end-to-end HTTP through App middleware + mux + module list handler |
| **`BenchmarkInMemoryCacheHIT`** | M1 | `middleware_test.go` | **Cache hit rate cost** — full middleware cache `ServeHTTP` on a warm entry; baseline for cache-layer overhead |
| `BenchmarkConsentFor_granted` | M6 | `benchmarks_test.go` | Cookie consent check — cost on every `SetCookieIfConsented` call |
| `BenchmarkRedirectStore_Get_exact` | M7 | `benchmarks_test.go` | O(1) exact-match lookup in a 100-entry redirect table |
| `BenchmarkRedirectStore_Get_prefix` | M7 | `benchmarks_test.go` | Prefix-match scan in a 50-entry redirect table (worst case: full scan) |
| `BenchmarkScheduler_tick_noop` | M8 | `benchmarks_test.go` | Scheduler tick cost when no items need publishing (steady-state) |
| `BenchmarkFeedStore_serve` | M5 | `benchmarks_test.go` | RSS feed serve — XML marshal + HTTP write for 20-item feed |

> Benchmarks in **bold** correspond to the three primary performance indicators:
> **request throughput**, **cache hit rate**, and **template render time**.

---

## Changelog

Entries are append-only. Each entry records: date, commit, Go version, CPU,
and the full result table.

---

### Run 1 — 2026-03-08

**Commit:** be4751f (M9 Step 2 — benchmarks_test.go created; HTMLTemplateRender added in Step 3 prep)
**Go:** go1.26.0 windows/amd64
**CPU:** Intel Core i5-9300HF @ 2.40GHz
**OS:** Windows (amd64)
**Flags:** `-benchtime=3s -benchmem`

| Benchmark | ns/op | B/op | allocs/op | Derived |
|-----------|------:|-----:|----------:|---------|
| `BenchmarkValidateStructCached` | 102 | 0 | 0 | — |
| `BenchmarkQueryScanCached` | 1,441 | 640 | 16 | — |
| `BenchmarkSignToken` | 2,233 | 1,296 | 16 | — |
| `BenchmarkBearerHMAC_verify` | 3,912 | 1,248 | 23 | — |
| `BenchmarkExcerpt` | 182 | 64 | 1 | — |
| `BenchmarkSchemaFor_Article` | 3,054 | 2,697 | 11 | — |
| `BenchmarkWriteSitemapFragment` | 159,492 | 58,096 | 224 | — |
| **`BenchmarkHTMLTemplateRender`** | **6,827** | **2,937** | **46** | **template render time** |
| `BenchmarkForgeMarkdown` | 138,706 | 77,935 | 1,536 | full markdown→HTML |
| **`BenchmarkModuleRequest`** (cached) | **5,716** | **7,294** | **37** | **~175k req/s (cache hit)** |
| **`BenchmarkApp_Handler`** (full stack) | **17,598** | **5,659** | **56** | **~57k req/s (end-to-end)** |
| **`BenchmarkInMemoryCacheHIT`** | **3,930** | **6,146** | **20** | **cache layer overhead ~4 µs/hit** |
| `BenchmarkConsentFor_granted` | 230 | 216 | 3 | — |
| `BenchmarkRedirectStore_Get_exact` | 29 | 0 | 0 | 0 allocs — map lookup |
| `BenchmarkRedirectStore_Get_prefix` | 80 | 0 | 0 | 0 allocs — slice scan |
| `BenchmarkScheduler_tick_noop` | 1,820 | 176 | 2 | — |
| `BenchmarkFeedStore_serve` | 61,348 | 31,888 | 88 | — |

#### Primary performance indicators

| Indicator | Benchmark | Result |
|-----------|-----------|--------|
| Request throughput — full stack | `BenchmarkApp_Handler` | **~57k req/s** (17,598 ns/op) |
| Request throughput — cache hit | `BenchmarkModuleRequest` | **~175k req/s** (5,716 ns/op) |
| Cache hit overhead | `BenchmarkInMemoryCacheHIT` | **3,930 ns/op** (~4 µs per cached response) |
| Template render time | `BenchmarkHTMLTemplateRender` | **6,827 ns/op** (~7 µs per page) |
