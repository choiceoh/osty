// Package cst contains the concrete syntax tree machinery for the Osty
// front end. Unlike the abstract AST in internal/ast, the CST is lossless:
// every source byte is reachable from either a token or a trivia run.
//
// The CST layer was introduced as part of the Red/Green tree refactor. Phase
// 1 provides the trivia extractor; subsequent phases build Green/Red node
// structures on top of it.
//
// Byte-coverage invariant: for any source src and tokens := selfhost.Lex(src),
// Extract(src, tokens) returns []Trivia such that:
//
//	every byte offset in [0, len(src)) is covered by exactly one token's
//	[Pos.Offset, End.Offset) OR exactly one Trivia's [Offset, Offset+Length).
//
// This invariant is the foundation for round-trip verification and for Phase
// 2+ Green node trivia attachment.
package cst
