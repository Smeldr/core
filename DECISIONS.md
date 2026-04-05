# Forge — Decision Log

This document is the permanent record of every architectural decision made for Forge.
Each entry captures what was decided, why, what was rejected, and what consequences follow.

**Format:** decisions are immutable once locked. New decisions are appended.
Revisions to existing decisions require a new entry that supersedes the original.

**How to use this document:**
- Before implementing a feature, check if a relevant decision exists
- Before changing an interface, check what depends on it here
- When onboarding (human or AI), read this before touching code

---

## Decision index

| # | Title | Status | Date |
|---|-------|--------|------|
| 1 | Node identity | Locked | 2025-06-01 |
| 2 | Storage model | Locked | 2025-06-01 |
| 3 | Head/SEO ownership | Locked | 2025-06-01 |
| 4 | Rendering model | Locked | 2025-06-01 |
| 5 | Cookie consent enforcement | Locked | 2025-06-01 |
| 6 | Context type | Locked | 2025-06-01 |
| 7 | AIDoc format | Locked | 2025-06-01 |
| 8 | llms.txt generation | Locked | 2025-06-01 |
| 9 | Sitemap strategy | Locked | 2025-06-01 |
| 10 | Validation API | Locked | 2025-06-01 |
| 11 | Internationalisation | Locked | 2025-06-01 |
| 12 | Image type | Locked | 2025-06-01 |
| 13 | RSS feeds | Locked | 2025-06-01 |
| 14 | Content lifecycle | Locked | 2025-06-01 |
| 15 | Role system | Locked | 2025-06-01 |
| 16 | Error handling model | Locked | 2025-06-01 |
| 17 | Redirects and content mobility | Locked | 2025-06-01 |
| 18 | Licensing strategy | Locked | 2025-06-01 |
| 19 | MCP (Model Context Protocol) support | Locked | 2025-06-01 |
| 20 | Configuration model | Locked | 2025-06-01 |
| 21 | forge.Context is an interface | Locked | 2025-06-01 |
| 22 | Storage interface and database drivers | Locked | 2025-06-01 |
| 25 | Token management | Locked | 2026-04-05 |