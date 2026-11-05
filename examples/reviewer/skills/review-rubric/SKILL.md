---
name: review-rubric
description: Sorts code review findings by severity. Use when writing up the findings of a review.
---

# Review rubric

Give each finding one severity:

- **Blocking**: wrong results, data loss, a security hole, or a crash on a path users take. The change can't merge as it is.
- **Important**: a real problem with a narrower trigger, such as a missed edge case, a race, or an error that's swallowed.
- **Nit**: naming, style or clarity. Mention these only when there are few other findings.

List blocking findings first. If there are none, say so in one line before the rest.
