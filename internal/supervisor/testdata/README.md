# Public helper control vectors

This fixture is a derived public artifact, not a byte-identical copy of the
original Rust fixture. The reference original SHA-256 is
36803ef0872efe4f1e377a01917e3313b4aa21bcd361945cc0e898d45c9b9a88.
Only the synthetic CONFIG endpoint hostname and its length/frame length fields
were changed to the reserved db.example.test domain. All non-CONFIG frames and
rejection cases retain their original bytes. The aggregate digest and file hash
were recomputed, using the original contract's digest algorithm/prime.

Tests pin both identities, compare literal vectors to the Go encoder, decode and
round-trip each valid frame, and reject the invalid cases. Normal Go tests are
self-contained; they do not require a companion Rust repository. This is codec
coverage, not a new claim of live-helper interoperability or deployment testing.
