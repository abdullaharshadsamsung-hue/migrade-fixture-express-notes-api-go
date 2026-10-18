# Migration Notes

**Model self-assessed confidence:** 96% (not a measured result)  
**Build at time of writing:** passed  
**Target unit tests:** passed  
**Behavior compared with the original:** passed

The final recommendation is in the pull request description.

---

## What was migrated

- `notes.js` → `internal/notes/store.go` (83% confidence) ⚠️ needs review
- `index.js` → `internal/httpapi/server.go` (89% confidence)
## Files requiring manual review

These files were migrated but scored below the confidence threshold.
Review them carefully before merging.

### `notes.js`
Confidence: 83%
Issues:
  - [info] Patch only allows title/body. Arbitrary keys and id overwrites are no longer merged, and a title can't be set to null or undefined.
  - [warning] A missing title is stored as an empty string and serialized as "title":"". The source leaves the key out entirely.
  - [info] Methods return copies instead of live references.
