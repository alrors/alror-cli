# Risk scoring

Every point comes from a named factor, so a reviewer can always see why a change scored what it did. Scores are capped to the range 0–100.

| Factor | Points |
| --- | --- |
| Diff size | +8 (≥50 lines), +15 (≥200), +22 (≥500), +30 (≥1000) |
| Many files | +8 (>20 files), +12 (>50) |
| Blast radius | +8 per extra service touched, max +20 |
| Critical service | +12 if any touched service is `critical` |
| Sensitive paths | +10 per area (auth, payment, migration, IAM, …), max +20 |
| No tests changed | +10 when code changed without tests |
| Well tested | −5 when test files ≥ half the code files |
| AI-authored | +10 from bot authors or trailers such as `Co-Authored-By: Claude` |
| Recent rollbacks | +6 per rollback on these services in 30 days, max +18 |
| Docs only | The score is 2, whatever else changed |

AI authorship is one input. It never blocks a change on its own.

## Levels and plans

| Level | Score | Plan |
| --- | --- | --- |
| Low | 0–34 | 25% → 100%, 5 min bake |
| Medium | 35–69 | 5% → 25% → 50% → 100%, 10 min bakes |
| High | 70–100 | 1% → 5% → 25% → 50% → 100%, 15 min bakes |
