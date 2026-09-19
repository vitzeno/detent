// Package gate is the safety boundary (PLAN.md §7). Deterministic by
// default — it never asks a model whether something is dangerous.
//
// Layer 1 (allowlist) and Layer 2 (argument validation) live here, built in
// §9 step 3 before any Jev call exists, on the principle that the gate must
// be solid against hand-written adversarial args before it needs to be
// solid against selected ones. Layer 3 (danger-tier UI, the confidence
// floor) and Layer 4 (the escalate-only Noul backstop) are later steps —
// Layer 3 needs the loop's write budget and confirm dialog to mean
// anything, and Layer 4 needs a Judge call to ask.
package gate
