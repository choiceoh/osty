package diag

// Stable diagnostic codes. The doc comment on each constant is the
// authoritative copy for ERROR_CODES.md AND the source for the Osty-side
// code→family manifest consumed by toolchain/diagnostic.osty. Regenerate
// both derived artifacts with `go generate ./internal/diag/...` whenever
// you add or edit a code.
//
//go:generate go run ../../cmd/codesdoc -in codes.go -w ../../ERROR_CODES.md
//go:generate go run ../../cmd/codesdoc -in codes.go -manifest ../../toolchain/diag_manifest.osty
//go:generate go run ../../cmd/codesdoc -in codes.go -manifest ../../examples/selfhost-core/diag_manifest.osty
//go:generate go run ../../cmd/codesdoc -in codes.go -harvest-cases ../../toolchain/diag_examples.osty

const (
	// Lexical.

	// A string literal reaches end-of-file without a closing quote.
	//
	// Example:
	//   let s = "hello
	// Fix: add the closing `"`. For multi-line text use triple-quoted strings.
	CodeUnterminatedString = "E0001"

	// Base prefixes must be lowercase.
	//
	// Spec: v0.2 R11 / v0.4 §1.6.1
	// Example:
	//   let n = 0X1F  // rejected
	// Fix: use `0x1F` / `0b1010` / `0o777`.
	CodeUppercaseBasePrefix = "E0002"

	// The escape sequence is unknown or references an invalid Unicode scalar value.
	//
	// Most commonly a surrogate code point.
	//
	// Spec: v0.4 §2.1
	// Example:
	//   let c = '\u{D800}'  // rejected
	// Fix: use a non-surrogate scalar (U+0..U+D7FF or U+E000..U+10FFFF).
	CodeUnknownEscape = "E0003"

	// A block comment reaches end-of-file without closing.
	//
	// Example:
	//   /* never closed
	// Fix: close the block with `*/`.
	CodeUnterminatedComment = "E0004"

	// A byte that does not begin any valid token.
	//
	// Commonly non-ASCII input outside of string literals.
	//
	// Fix: remove the stray byte or move it inside a string.
	CodeIllegalCharacter = "E0005"

	// Triple-quoted string violates the indent rules.
	//
	// The opening `"""` must be followed by a newline, every content line
	// must begin with the closing-line's whitespace prefix, and the
	// closing `"""` must be on its own line.
	//
	// Spec: v0.4 §1.6.3
	// Fix: realign the content and closing delimiter per §1.6.3.
	CodeBadTripleString = "E0006"

	// The `=>` (fat-arrow) token was removed from the grammar.
	//
	// `match` arms and every other arrow position use `->` instead.
	// Any occurrence of `=>` in source is a lex error (O7, §1.7).
	//
	// Spec: v0.4 §1.7, OSTY_GRAMMAR_v0.4 O7
	// Example:
	//   match x { 0 => "zero", _ => "other" }  // rejected
	// Fix: replace `=>` with `->`.
	CodeFatArrowRemoved = "E0007"

	// A numeric literal places `_` outside the allowed between-digits position.
	//
	// `_` may only appear between two digits of the same base. Leading
	// underscores after a base prefix, trailing underscores, consecutive
	// underscores, and underscores adjacent to `.` or `e`/`E` are all rejected.
	//
	// Spec: v0.4 §1.6.1
	// Example:
	//   let a = 1_            // trailing
	//   let b = 0x_FF         // after base prefix
	//   let c = 1__000        // consecutive
	// Fix: place `_` only between two digits.
	CodeBadNumericSeparator = "E0008"

	// A char or byte literal is empty, holds more than one Unicode scalar,
	// or holds a non-ASCII scalar where only bytes are permitted.
	//
	// Char literals hold exactly one Unicode scalar; byte literals (`b'...'`)
	// hold exactly one ASCII scalar.
	//
	// Spec: v0.4 §1.6.4
	// Example:
	//   let a = ''             // empty
	//   let b = b'\u{1F600}'   // non-ASCII byte
	//   let c = 'ab'           // multiple scalars
	// Fix: put exactly one Unicode scalar between the quotes
	//      (ASCII only for `b'...'`).
	CodeBadCharLiteral = "E0009"

	// Declarations & statements.

	// A token that cannot begin a top-level declaration appeared where one was expected.
	//
	// Fix: precede the token with a valid declaration keyword (`fn`, `let`, `struct`, …).
	CodeExpectedDecl = "E0100"

	// Functions declared inside `use go "..."` must not have a body.
	//
	// They forward to the imported Go function.
	//
	// Spec: v0.4 R17
	// Example:
	//   use go "net/http" {
	//       fn Get(url: String) -> String { "x" }  // rejected
	//   }
	// Fix: drop the body — keep only the signature.
	CodeUseGoFnHasBody = "E0101"

	// Structs inside `use go { ... }` blocks mirror Go field layout only.
	//
	// Methods live on the Go side.
	//
	// Spec: v0.4 R16
	// Fix: move the method definition to the Go file that owns the type.
	CodeUseGoStructHasMethod = "E0102"

	// A feature not permitted inside a `use go` block.
	//
	// Generics, parameter defaults, enums, interfaces, type aliases, and
	// bodies on `fn` are all rejected.
	//
	// Fix: simplify the declaration to a bare field layout or signature.
	CodeUseGoUnsupported = "E0103"

	// A `use` path mixes dotted and urlish forms.
	//
	// A path is either dotted (`std.fs`) OR urlish (`github.com/x/y`) —
	// the two cannot mix.
	//
	// Spec: v0.4 R15
	// Fix: choose one form for the whole path.
	CodeUsePathMixed = "E0104"

	// `else` appears on a new line.
	//
	// It must sit on the same line as the closing `}` of the `if` body.
	//
	// Spec: v0.4 O2
	// Example:
	//   if cond {
	//       ...
	//   }
	//   else {  // rejected
	//       ...
	//   }
	// Fix: move `else` onto the same line as the preceding `}`.
	CodeElseAcrossNewline = "E0105"

	// Parameter or field default is not a literal.
	//
	// Defaults must be restricted literal forms (literal, `-` numeric,
	// `None`, `Ok(lit)`, `Err(lit)`, `[]`, `{:}`, `()`).
	//
	// Spec: v0.4 R18
	// Example:
	//   fn connect(t: Int = computeTimeout()) {}  // rejected
	// Fix: use a literal default, or move the computation into the body.
	CodeDefaultExprNotLiteral = "E0106"

	// Expressions.

	// Comparison or range operators are non-associative.
	//
	// Spec: v0.4 R1
	// Example:
	//   a < b < c      // rejected
	//   0..10..20      // rejected
	// Fix: parenthesize — `(a < b) && (b < c)`.
	CodeNonAssocChain = "E0200"

	// `::` is reserved for turbofish and must be followed by `<`.
	//
	// Spec: v0.4 O6
	// Example:
	//   foo::bar()     // rejected — did you mean `foo.bar()`?
	// Fix: use `.` for member access or `::<T>` for type application.
	CodeTurbofishMissingLT = "E0201"

	// Method chains must continue with a leading dot on the next line.
	//
	// A trailing `.` then newline is a syntax error.
	//
	// Spec: v0.4 O3
	// Fix: move the `.` to the start of the continuation line.
	CodeTrailingDot = "E0202"

	// A closure with an explicit return type must have a block body.
	//
	// Spec: v0.4 R25
	// Example:
	//   let f = |x: Int| -> Int x * 2       // rejected
	//   let f = |x: Int| -> Int { x * 2 }   // ok
	// Fix: wrap the expression in `{ ... }`.
	CodeClosureRetReqBlock = "E0203"

	// Fallback for expression-position tokens that don't begin a valid primary expression.
	//
	// Fix: check for a missing operand, operator, or brace.
	CodeUnexpectedToken = "E0204"

	// Types & patterns.

	// A token that cannot begin a type appeared in a type position.
	//
	// Fix: supply a type name, `Self`, or a parenthesized type form.
	CodeExpectedType = "E0300"

	// A token that cannot begin a pattern appeared in a pattern position.
	//
	// Fix: supply a literal, variant, struct, tuple, or `_` pattern.
	CodeExpectedPattern = "E0301"

	// Annotations.

	// The annotation name is not recognized.
	//
	// Only `#[json(...)]` and `#[deprecated(...)]` are defined today.
	//
	// Spec: v0.4 R26
	// Fix: remove the annotation or use one of the recognized names.
	CodeUnknownAnnotation = "E0400"

	// Name resolution.

	// The referenced identifier is not in scope.
	//
	// Typo suggestions use edit distance — the diagnostic says
	// "did you mean `X`?" when a nearby name exists.
	//
	// Fix: import the name, or correct the spelling.
	CodeUndefinedName = "E0500"

	// The same name is declared twice in the same scope.
	//
	// Scopes affected: top-level, struct fields, enum variants,
	// methods, or a single block.
	//
	// Fix: rename one of the declarations.
	CodeDuplicateDecl = "E0501"

	// A name is used in a position that disagrees with its declaration.
	//
	// For example, a function name used where a type is expected.
	//
	// Fix: use a name of the right kind, or adjust the expected position.
	CodeWrongSymbolKind = "E0502"

	// `self` is only valid as the first parameter of a method and inside that method's body.
	//
	// Fix: move the reference inside a method, or rename the identifier.
	CodeSelfOutsideMethod = "E0503"

	// `Self` is only valid inside a `struct`, `enum`, or `interface` body.
	//
	// Fix: replace with the actual type name outside the declaration.
	CodeSelfTypeOutside = "E0504"

	// The referenced package cannot be found.
	//
	// Spec: v0.4 §5
	// Fix: check the `use` path and verify the package is on disk or in the manifest.
	CodeUnknownPackage = "E0505"

	// A `use` graph contains a cycle.
	//
	// Package A imports B which eventually imports A. The resolver
	// breaks the cycle and reports the first edge that closes it.
	//
	// Spec: v0.4 §5.3
	// Fix: extract shared declarations into a third package that both sides import.
	CodeCyclicImport = "E0506"

	// A cross-package reference targets a non-`pub` item.
	//
	// Private items are visible only to other files in the same package.
	//
	// Spec: v0.4 §5.2
	// Fix: add `pub` to the declaration, or move the caller into the same package.
	CodePrivateAcrossPackages = "E0507"

	// Package member access names an item that isn't exported.
	//
	// The member might be private, misspelled, or from a different package.
	//
	// Spec: v0.4 §5.2
	// Fix: verify the name is `pub` and matches the exported spelling.
	CodeUnknownExportedMember = "E0508"

	// A `use std.*` import cannot be resolved because the stdlib provider is unavailable.
	//
	// The compiler runs with a lazily-loaded stdlib descriptor; if it
	// hasn't been loaded for the current invocation, `std.*` names
	// fall back to this error.
	//
	// Fix: invoke the compiler with the standard entrypoint that wires up the stdlib.
	CodeStdlibNotAvailable = "E0509"

	// Control flow / context.

	// `break` must be inside a `for` loop.
	//
	// Fix: enclose the statement in a `for` body, or remove it.
	CodeBreakOutsideLoop = "E0600"

	// `continue` must be inside a `for` loop.
	//
	// Fix: enclose the statement in a `for` body, or remove it.
	CodeContinueOutsideLoop = "E0601"

	// `return` must be inside a function body.
	//
	// Scripts count — their top-level statements are wrapped in an implicit `main()`.
	//
	// Fix: move the `return` into a function, or drop it from a library file.
	CodeReturnOutsideFn = "E0602"

	// `defer` must be inside a function body.
	//
	// Fix: move the `defer` into a function.
	CodeDeferOutsideFn = "E0603"

	// `_` is a pattern wildcard; it cannot stand in for a value in an expression.
	//
	// Example:
	//   let x = _  // rejected
	// Fix: for ignored bindings use `let _ = expr`.
	CodeWildcardInExpr = "E0604"

	// Every alternative of an or-pattern must bind the same names.
	//
	// Spec: v0.4 §4.3.1
	// Example:
	//   match e {
	//       A(x) | B(x, y) -> ...   // rejected: `y` not bound by A
	//   }
	// Fix: rebalance the alternatives to bind the same names.
	CodeOrPatternBindingMismatch = "E0605"

	// An interface default method may not access fields on `self`.
	//
	// The interface has no view of the implementing type's layout.
	//
	// Spec: v0.4 §2.6.2
	// Fix: call other interface methods instead of reading fields directly.
	CodeInterfaceDefaultField = "E0606"

	// The annotation's target is not in its permitted set.
	//
	// `#[json]` only attaches to struct fields; `#[deprecated]` to
	// top-level declarations and methods; neither attaches to `use`.
	//
	// Spec: v0.4 §18.1
	// Fix: move the annotation to a permitted target.
	CodeAnnotationBadTarget = "E0607"

	// Bare `defer` at the top level of a script is rejected.
	//
	// Spec: v0.4 §6 / §18.3
	// Fix: wrap the cleanup in an explicit `fn` or move it inside an existing function body.
	CodeDeferAtScriptTop = "E0608"

	// The same annotation name may not appear twice on a single target.
	//
	// Spec: v0.4 §18.1
	// Example:
	//   #[deprecated]
	//   #[deprecated]           // rejected
	//   pub fn f() {}
	// Fix: remove the duplicate.
	CodeDuplicateAnnotation = "E0609"

	// Type checking.

	// Wrong type in assignment, return, or argument position.
	//
	// Fix: convert or choose a compatible type.
	CodeTypeMismatch = "E0700"

	// Call arity mismatch.
	//
	// Fix: pass the expected number of arguments.
	CodeWrongArgCount = "E0701"

	// `foo.bar` — no such field.
	//
	// Fix: check the field name against the struct definition.
	CodeUnknownField = "E0702"

	// `foo.bar()` — no such method.
	//
	// Fix: verify the method exists on the type or its implemented interfaces.
	CodeUnknownMethod = "E0703"

	// Call target isn't a function, method, or variant.
	//
	// Fix: only functions, methods, and tuple-struct/variant constructors are callable.
	CodeNotCallable = "E0704"

	// `x[i]` — type has no indexing.
	//
	// Fix: switch to a type that supports indexing (list, map, string).
	CodeNotIndexable = "E0705"

	// `T { ... }` — `T` isn't a struct.
	//
	// Fix: use a struct type, or construct via the correct factory.
	CodeNotAStruct = "E0706"

	// Struct literal names a field the struct doesn't have.
	//
	// Fix: remove the extra field or correct its name.
	CodeUnknownStructField = "E0707"

	// Struct literal omits a required field.
	//
	// Fix: add the missing field or give it a default in the declaration.
	CodeMissingStructField = "E0708"

	// Enum variant payload has the wrong arity or shape.
	//
	// Fix: match the payload signature declared on the variant.
	CodeVariantShape = "E0709"

	// Pattern names something that isn't a variant.
	//
	// Fix: use a real variant of the scrutinee's enum.
	CodeNotAVariant = "E0710"

	// Match arms don't unify to a single result type.
	//
	// Fix: coerce arms to a common type or split the match.
	CodeMatchArmMismatch = "E0711"

	// `if` / `else` branches don't unify.
	//
	// Fix: give both branches the same type, or use `if` as a statement.
	CodeIfBranchMismatch = "E0712"

	// Operator not defined on the operand types.
	//
	// Fix: convert an operand or use a different operator.
	CodeBinaryOpUntyped = "E0713"

	// Unary operator not defined on the operand type.
	//
	// Fix: check that the type supports the operator (e.g. `Bool` for `!`, `Int`/`Float` for `-`).
	CodeUnaryOpUntyped = "E0714"

	// `if` / `for` condition isn't `Bool`.
	//
	// Fix: produce a `Bool` from the expression (e.g. `x != 0`).
	CodeConditionNotBool = "E0715"

	// `for x in e` — `e` has no iterator.
	//
	// Fix: iterate over a list, map, range, or `Iterator`-implementing type.
	CodeNotIterable = "E0716"

	// `?` used on a non-`Result` / non-`Option` value.
	//
	// Fix: only use `?` on fallible types.
	CodeQuestionNotPropagate = "E0717"

	// `?` used where the enclosing return type cannot hold the propagated error.
	//
	// Fix: change the fn return type to `Result<...>` / `Option<...>`.
	CodeQuestionBadReturn = "E0718"

	// `?.` used on a non-`Option` receiver.
	//
	// Fix: drop the `?` (plain `.`) or wrap the receiver in `Option`.
	CodeOptionalChainOnNon = "E0719"

	// `??` left-hand side is not `Option`.
	//
	// Fix: change the LHS to an optional, or replace `??` with another fallback form.
	CodeCoalesceNonOptional = "E0720"

	// Numeric literal does not fit in the inferred type.
	//
	// Example:
	//   let x: UInt8 = 300  // rejected
	// Fix: widen the target type or shrink the literal.
	CodeNumericLitRange = "E0721"

	// Literal pattern type does not match the scrutinee.
	//
	// Fix: use a pattern whose type matches the value.
	CodeLitPatternMismatch = "E0722"

	// Range pattern requires an `Ordered` scrutinee.
	//
	// Fix: switch to an ordered type (numbers, chars) or explode the range.
	CodeRangePatternNonOrd = "E0723"

	// LHS of `=` is not assignable.
	//
	// Fix: assign into a `let mut` binding, a struct field, or an index.
	CodeAssignTarget = "E0724"

	// Assign into a non-`mut` binding, or into a field of a non-`mut` receiver.
	//
	// Fix: add `mut` to the binding, or rebind via `let`.
	CodeMutabilityMismatch = "E0725"

	// Return expression doesn't match the fn signature.
	//
	// Fix: return a value of the declared type, or change the signature.
	CodeReturnTypeMismatch = "E0726"

	// Wrong number of type arguments for a generic.
	//
	// Fix: supply exactly as many type args as the generic declares (or omit for inference).
	CodeGenericArgCount = "E0727"

	// `Enum.Variant` — `Variant` isn't declared on the enum.
	//
	// Fix: check the variant name against the enum definition.
	CodeUnknownVariant = "E0728"

	// `<`, `<=`, `>`, `>=` used on a non-`Ordered` type.
	//
	// Fix: only compare types that implement `Ordered`.
	CodeTypeNotOrdered = "E0729"

	// `==` / `!=` used on a non-`Equal` type.
	//
	// Fix: only compare types that implement `Equal`.
	CodeTypeNotEqual = "E0730"

	// Match doesn't cover every case of the scrutinee.
	//
	// Fix: add the missing arms or a catch-all `_ ->` branch.
	CodeNonExhaustiveMatch = "E0731"

	// Keyword argument names no such parameter.
	//
	// Fix: check the parameter name against the fn signature.
	CodeKeywordArgUnknown = "E0732"

	// Positional argument appears after a keyword argument.
	//
	// Fix: move all positional arguments before the first keyword argument.
	CodePositionalAfterKw = "E0733"

	// Same parameter passed twice (positionally and by name, or two keyword args).
	//
	// Fix: pass each parameter at most once.
	CodeDuplicateArg = "E0734"

	// Interpolated expression doesn't implement `ToString`.
	//
	// Fix: call `.toString()` explicitly or wrap in `str(...)`.
	CodeInterpolationNonStr = "E0735"

	// `for-in` receiver doesn't implement the `Iterator` protocol.
	//
	// The resolver accepts any value the checker couldn't disprove, but
	// the checker requires either a built-in iterable or a type that
	// implements `Iterator<Item = T>` / `next()`.
	//
	// Fix: implement `Iterator` on the type, or convert to a known iterable.
	CodeIterableNotProtocol = "E0736"

	// `ch <- v` where `v`'s type doesn't match the channel element type.
	//
	// Fix: send a value of the channel's `Chan<T>` element type.
	CodeChannelWrongValue = "E0737"

	// `ch <- v` where `ch` isn't a `Chan<T>`.
	//
	// Fix: use a channel on the left-hand side of `<-`.
	CodeChannelNotChan = "E0738"

	// Annotation argument has the wrong type.
	//
	// Example:
	//   #[json(key = 42)]   // `key` expects a String
	// Fix: pass an argument whose type matches the annotation's schema.
	CodeAnnotationBadArg = "E0739"

	// Match arm is unreachable because a previous arm fully covers its cases.
	//
	// Fix: merge or remove the shadowed arm.
	CodeUnreachableArm = "E0740"

	// Pattern in an irrefutable position can fail to match.
	//
	// Three spec sites require irrefutable patterns: `let p = e` (§A.5
	// let bindings), `for p in e` (§A.5 for-in bindings), and closure
	// parameters (G16 — destructured at every call site). Irrefutable
	// means: identifiers, `_`, tuples/structs made only of irrefutable
	// sub-patterns, or `name @ irrefutable`.
	//
	// Spec: v0.4 §A.5, G16
	// Fix: accept the value with an irrefutable pattern, then use `match` or `if let` inside the body for the refutable cases.
	CodeRefutablePattern = "E0741"

	// Generic function or method is referenced without being called.
	//
	// Osty v0.4 does not have first-class polymorphic function values;
	// generic callables must be instantiated by a call site, or wrapped
	// in a closure that fixes the type arguments.
	//
	// Spec: v0.4 G14
	// Fix: call the generic directly, or write a wrapper closure such as `|x| f::<Int>(x)`.
	CodeGenericCallableReference = "E0742"

	// Structured-concurrency capability escapes its group scope.
	//
	// `Handle<T>` and `TaskGroup` are non-escaping capabilities. They may
	// be used in the same `taskGroup` scope, joined/cancelled there, and
	// passed to helpers that do not store or return them. Returning one,
	// storing one in a field/collection, sending one over a channel, or
	// capturing one in an escaping closure is rejected.
	//
	// Spec: v0.4 G13
	// Fix: join/use the handle inside the `taskGroup` closure and return an ordinary value.
	CodeCapabilityEscape = "E0743"

	// Operator cannot be applied to the operand's type.
	//
	// Currently the runtime's catch-all for unary (`!`, `-`, `+`, `~`),
	// binary arithmetic / bitwise / comparison / logical, `??` coalesce,
	// `<-` channel send, and `in` membership type mismatches. The more
	// specialized codes E0713/E0714/E0720/E0737/E0738 are reserved but
	// not currently emitted — callers should expect E0744 today.
	//
	// Fix: convert an operand, or switch to an operator defined on the type.
	CodeOperandType = "E0744"

	// Resolver could not find a name in the current scope.
	//
	// Emitted by the checker when an identifier reference doesn't match
	// any local binding or top-level function in scope (the resolver
	// passed it through as a last-chance lookup, typically because of
	// missing imports or a typo).
	//
	// Fix: check spelling, imports, and receiver type; if it's a method, write the receiver explicitly.
	CodeUnknownName = "E0745"

	// Struct literal (or declaration) names the same field twice.
	//
	// Fix: remove the duplicate or rename one of the entries.
	CodeDuplicateField = "E0746"

	// Two methods on the same type share a name.
	//
	// Fix: rename one of the methods, or merge their bodies.
	CodeDuplicateMethod = "E0747"

	// A type parameter could not be inferred from the arguments.
	//
	// Fix: supply the type argument explicitly via turbofish `f::<T>(...)`, or pass an argument whose type constrains the parameter.
	CodeCannotInferTyParam = "E0748"

	// A type argument violates a generic bound.
	//
	// Example:
	//   fn f<T: Ordered>(x: T) { ... }
	//   f("hello")     // String is not Ordered → rejected
	//
	// Fix: switch to a type that satisfies the bound, or relax the bound.
	CodeGenericBoundViolation = "E0749"

	// A concrete type does not satisfy a required interface.
	//
	// Osty's interfaces are structural — every method in the interface
	// must be present on the concrete type with a matching signature.
	//
	// Fix: add the missing methods, or switch to a type that already satisfies the interface.
	CodeInterfaceNotSatisfied = "E0751"

	// Closure parameter lacks a type annotation in a context where it
	// cannot be inferred (no expected-type hint from the call site).
	//
	// Fix: annotate the parameter explicitly (`|x: Int| ...`), or use the closure in a position that provides an expected type.
	CodeClosureAnnotationRequired = "E0752"

	// Pattern structure does not match the scrutinee's type.
	//
	// Covers literal / range / tuple-arity / struct / variant pattern
	// shape errors — a broader category than the literal-type mismatch
	// E0722: the scrutinee might be an Int where the pattern is a tuple,
	// or the scrutinee a tuple of arity 3 where the pattern is arity 2.
	//
	// Fix: rewrite the pattern to match the scrutinee's shape, or guard with a type-narrowing arm above it.
	CodePatternShapeMismatch = "E0753"

	// Deprecation warning.

	// Use site references an item marked `#[deprecated]`.
	//
	// Emitted as a `diag.Warning`. Tooling can promote it to error via
	// build configuration.
	//
	// Spec: v0.4 §3.8.2
	// Fix: migrate to the replacement noted in the `#[deprecated]` annotation.
	CodeDeprecatedUse = "W0750"

	// Type checking — control flow & const fn.

	// Control flow diagnostics (E0760-E0769).
	//
	// CodeUnreachableCode: a statement appears after a divergent
	// construct (return, break, continue, or an expression of type
	// Never) and therefore can never execute.
	// Spec: v0.4 §4 control flow, §2.1 Never
	// Fix: delete the dead statement or move it above the divergent one.
	CodeUnreachableCode = "E0760"

	// CodeMissingReturn: a non-unit function's body could reach its
	// end without producing a value matching the return type.
	// Spec: v0.4 §3.1
	// Fix: add an explicit `return` or make the final expression the
	//      function's result.
	CodeMissingReturn = "E0761"

	// CodeDefaultNotLiteral: a default argument expression is not a
	// literal (§3.1 forbids computed defaults).
	// v0.5 (G21): the literal definition is extended to include struct
	// literals whose fields are themselves literals, and the return
	// value of a `const fn` call. Expressions outside this set still
	// emit this code.
	// Fix: replace the expression with a numeric, string, char, byte,
	//      bool, `None`, `Ok(literal)`, `Err(literal)`, `[]`, `{:}`,
	//      `()`, a struct literal of literals, or a `const fn` call.
	CodeDefaultNotLiteral = "E0762"

	// CodeUndefinedLabel: `break 'label` / `continue 'label` referred
	// to a label that is not in scope (not attached to any enclosing
	// loop).
	// v0.5 (G24) §4.4.
	// Fix: add `'label:` to the intended loop, or remove the label
	//      from the break/continue.
	CodeUndefinedLabel = "E0763"

	// CodeLabelShadow: a `'label:` reuses a name already in scope from
	// an outer loop, making `break 'label` in the inner loop ambiguous.
	// v0.5 (G24) §4.4.
	// Fix: rename one of the two labels so each name is unique within
	//      the nested stack.
	CodeLabelShadow = "E0764"

	// CodeConstFnDisallowed: the body of a `const fn` contains a
	// construct outside the §3.1.1 capability matrix. Allowed:
	// literals, arithmetic / comparison / boolean on numeric / bool,
	// `let` bindings, parameter references, references to top-level
	// `pub? let` of DefaultLiteral type, direct calls to other
	// `const fn` (acyclic), struct / enum-variant / tuple / list / map
	// construction with all-const operands. Forbidden: control flow
	// (`if` / `match` / `for` / `loop` / `while` / `return` /
	// `defer` / `?`), closures, method calls, operator overloads,
	// string concatenation / interpolation, `let mut` / assignment,
	// FFI symbols, `panic` / `todo` / `abort`, recursion, I/O.
	// v0.5 (G21) §3.1.1.
	// Fix: rewrite the body using only matrix-allowed constructs, or
	//      drop `const` if the function is only needed at runtime.
	CodeConstFnDisallowed = "E0766"

	// CodeConstFnCycle: the `const fn` call graph contains a cycle —
	// either direct recursion (`const fn f() { f() }`) or a transitive
	// loop between two or more `const fn`s. Reported at the resolver
	// pass before type checking.
	// v0.5 (G21) §3.1.1.
	// Fix: break the cycle. Recursion is not available in `const fn`;
	//      express the computation iteratively via a runtime function,
	//      or precompute the value as a `pub let` binding.
	CodeConstFnCycle = "E0767"

	// CodeConstFnGeneric: a `const fn` declaration carries type
	// parameters (`const fn f<T>(...)`). Generic `const fn` would
	// require a monomorphizing const-evaluation engine, which Osty
	// does not provide.
	// v0.5 (G21) §3.1.1.
	// Fix: declare a separate `const fn` per concrete type, or drop
	//      `const` and use an ordinary generic function at runtime.
	CodeConstFnGeneric = "E0768"

	// v0.5 additions (G20-G35). The following codes extend the E07xx
	// band for numeric widening, operator overloading, enum
	// discriminants, and label/loop control flow. Module-resolution
	// additions for `pub use` re-export and scoped imports live in
	// the E055x band. `#[cfg(...)]` key validation lives in E0405.
	//
	// Free slots claimed: E0754-E0759 (typecheck), E0765-E0768
	// (control flow; E0766-E0768 are `const fn` validation, §3.1.1),
	// E0552-E0554 (name resolution), E0405 (imports). E0769 remains
	// free in the control-flow band. The E0770-E0772 slots are
	// occupied by §19 runtime sublanguage diagnostics
	// (CodeRuntimePrivilegeViolation, CodePodShapeViolation,
	// CodeNoAllocViolation) defined below.

	// CodeOpAnnotationBadSignature: a method carrying `#[op(X)]`
	// does not match the required shape for operator X (wrong
	// parameter count, wrong self-position, wrong return type).
	// v0.5 (G35) §3.1.
	// Fix: for binary `+`, `-`, `*`, `/`, `%`, declare
	//      `fn(self, other: Rhs) -> Self` (or `Out` for `*`). For
	//      unary `-`, declare `fn neg(self) -> Self`.
	CodeOpAnnotationBadSignature = "E0754"

	// CodeOpDuplicate: two methods on the same type carry the same
	// `#[op(X)]` annotation.
	// v0.5 (G35) §3.1.
	// Fix: remove one of the duplicate operator implementations.
	CodeOpDuplicate = "E0755"

	// CodeOpNotAllowed: `#[op(...)]` names an operator outside the
	// permitted set `{+, -, *, /, %}` (binary) and `{-}` (unary).
	// `==`, `!=`, `<`, `<=`, `>`, `>=`, `[]`, `()`, `<<`, `>>`,
	// `&`, `|`, `^` cannot be overloaded.
	// v0.5 (G35) §3.1, §14.1.
	// Fix: implement equality/ordering via the `Equal` / `Ordered`
	//      interfaces; use named methods for indexing and bitwise ops.
	CodeOpNotAllowed = "E0756"

	// CodeAsQuestionBadType: `expr as? T` applied to a value whose
	// static type is not a known `Error` implementor, or `T` is not
	// a concrete type implementing `Error`.
	// v0.5 (G27) §4.9.
	// Fix: call `.downcast::<T>()` via method syntax on a non-error
	//      value, or match structurally.
	CodeAsQuestionBadType = "E0757"

	// CodeEnumDiscriminantOnPayload: `enum X: Int { V(T) = N }` is
	// rejected — discriminant assignment is only legal on payload-free
	// variants.
	// v0.5 (G31) §3.5.
	// Fix: drop the payload (making it a unit variant) or drop the
	//      `= N` assignment.
	CodeEnumDiscriminantOnPayload = "E0758"

	// CodeEnumDiscriminantDuplicate: two variants of the same enum
	// are assigned the same discriminant value.
	// v0.5 (G31) §3.5.
	// Fix: pick distinct values for each variant.
	CodeEnumDiscriminantDuplicate = "E0759"

	// CodeImplicitNarrowingConversion: an expression site required an
	// implicit numeric narrowing (e.g. `Int64 -> Int32`, `Float64 ->
	// Int`). v0.5 allows lossless widening only; narrowing must be
	// spelled explicitly.
	// v0.5 (G34) §2.2a.
	// Fix: call one of `.toInt32()`, `.toInt16()`, `.toInt8()`,
	//      `.toIntTrunc()`, `.toIntRound()`, `.toIntFloor()`,
	//      `.toIntCeil()`, or `.toFloat32()` to make the intent
	//      explicit.
	CodeImplicitNarrowingConversion = "E0765"

	// Name resolution — re-exports & scoped imports.

	// Module-resolution diagnostics for v0.5 re-export and scoped
	// imports (G28, G30).

	// CodeReexportCycle: `pub use` re-export chain contains a cycle.
	// v0.5 (G30) §5.
	// Fix: break the cycle by rerouting one of the re-exports through
	//      the original definition rather than another re-export.
	CodeReexportCycle = "E0552"

	// CodeReexportPrivate: `pub use` attempted to re-export a private
	// symbol.
	// v0.5 (G30) §5.
	// Fix: make the source symbol `pub`, or drop the `pub use`.
	CodeReexportPrivate = "E0553"

	// CodeUseDuplicateName: a scoped `use path::{a, a}` names the same
	// identifier twice, or two separate imports introduce the same
	// local binding.
	// v0.5 (G28) §5.
	// Fix: remove the duplicate or use `as` to rename one side.
	CodeUseDuplicateName = "E0554"

	// Annotations — cfg.

	// CodeCfgUnknownKey: `#[cfg(key = "...")]` used an unknown key.
	// v0.5 (G29) recognises only `os`, `target`, `arch`, `feature`.
	// Fix: use one of the supported keys; unrecognised keys are not a
	//      forward-compatibility hatch.
	CodeCfgUnknownKey = "E0405"

	// Runtime sublanguage.

	// CodePodShapeViolation: a `struct` carrying `#[pod]` violates the
	// LANG_SPEC §19.4 plain-old-data rule. The diagnostic names the
	// first offending field for non-Pod field types, or the first
	// generic parameter that lacks a `T: Pod` bound for unbounded
	// generic structs.
	//
	// Spec: v0.5 §19.4
	// Fix: replace the offending field's type with a `Pod` type
	//      (primitives, `RawPtr`, other `#[pod] #[repr(c)]` structs,
	//      tuples of `Pod`, `Option<T: Pod>`); for unbounded generic
	//      structs, add `T: Pod` to every type parameter.
	CodePodShapeViolation = "E0771"

	// CodeRuntimePrivilegeViolation: a runtime-sublanguage surface is
	// used outside a privileged package. The surface includes the
	// annotations `#[intrinsic]`, `#[pod]`, `#[repr(c)]`, `#[export(...)]`,
	// `#[c_abi]`, and `#[no_alloc]`; the opaque type `RawPtr`; the
	// marker trait `Pod`; and any `use std.runtime.*` import. A package
	// is privileged when its fully-qualified path begins with
	// `std.runtime.` or when its manifest declares
	// `[capabilities] runtime = true` and loads from the toolchain
	// workspace root.
	//
	// Spec: v0.5 §19.2
	// Fix: move the code into `std.runtime.*`, or (for toolchain
	//      workspace packages) add `[capabilities] runtime = true` to
	//      the package's `osty.toml`. User code has no way to opt in;
	//      refactor it to use ordinary managed types.
	CodeRuntimePrivilegeViolation = "E0770"

	// CodeNoAllocViolation: a function carrying `#[no_alloc]` contains
	// an expression that requires the managed allocator (string
	// interpolation, list/map/set literal, non-Pod struct literal,
	// non-runtime enum construction, Builder use), or calls a function
	// that is not itself `#[no_alloc]`. LANG_SPEC §19 allocates this
	// band at E0770-E0779; the control-flow band E0760-E0769 above is
	// already in use.
	//
	// Spec: v0.5 §19.6.1
	// Fix: replace the offending expression with raw-memory primitives
	//      (`std.runtime.raw.*`), pre-allocated buffers, plain string
	//      literals, or restructure the call so the callee is also
	//      `#[no_alloc]`.
	CodeNoAllocViolation = "E0772"

	// CodeIntrinsicNonEmptyBody: a function carrying `#[intrinsic]`
	// has a non-empty body. LANG_SPEC §19.6 mandates that intrinsic
	// declarations are body-less stubs whose implementation is
	// supplied by the lowering layer at each call site. Permitting
	// a body would be misleading because the backend ignores it
	// (the MIR pipeline bails on intrinsic functions; the legacy
	// path uses the body for its own reasons but the LLVM emit
	// would still need per-intrinsic dispatch to produce correct
	// code). The accepted forms per the spec are `fn foo() -> T`
	// (signature only) or `fn foo() -> T {}` (empty block).
	//
	// Spec: v0.5 §19.6
	// Fix: drop the body — keep only the signature, or write an
	//      empty `{}`. The actual implementation lives in the
	//      backend lowering table (§19.7).
	CodeIntrinsicNonEmptyBody = "E0773"

	// Manifest — TOML syntax.

	// Fallback TOML syntax error in `osty.toml`.
	//
	// Fix: validate the file with a TOML linter; check for quotes and brackets.
	CodeManifestSyntax = "E2000"

	// String or inline table in `osty.toml` is unterminated.
	//
	// Fix: close the quote or table on the same line.
	CodeManifestUnterminated = "E2001"

	// Same key set twice in the same TOML table.
	//
	// Fix: keep one key; remove or rename the duplicate.
	CodeManifestDuplicateKey = "E2002"

	// Same `[table]` header declared twice.
	//
	// Fix: consolidate the two sections into one.
	CodeManifestDuplicateTable = "E2003"

	// Unknown `\escape` in a TOML string.
	//
	// Fix: use a supported escape or a literal string (`'...'`).
	CodeManifestBadEscape = "E2004"

	// Manifest — schema.

	// `osty.toml` has no `[package]` table.
	//
	// Fix: add a `[package]` section with at least `name` and `version`.
	CodeManifestMissingPackage = "E2010"

	// A required field is absent from the manifest.
	//
	// Fix: add the named field under the expected table.
	CodeManifestMissingField = "E2011"

	// Unrecognized key for a known table.
	//
	// Fix: remove the key or rename it to a documented one.
	CodeManifestUnknownKey = "E2012"

	// Manifest field has the wrong TOML type.
	//
	// Fix: quote strings, wrap arrays in `[]`, and use bare identifiers where required.
	CodeManifestFieldType = "E2013"

	// `[package]` name does not satisfy identifier rules.
	//
	// Fix: use a lowercase name with letters, digits, and `-` / `_`.
	CodeManifestBadName = "E2014"

	// `version` is not a valid semver triple.
	//
	// Fix: set `version = "MAJOR.MINOR.PATCH"` (pre-release suffix allowed).
	CodeManifestBadVersion = "E2015"

	// `edition` is not a recognized value.
	//
	// Fix: pick a supported edition (e.g. `"2024"`).
	CodeManifestBadEdition = "E2016"

	// Dependency entry is missing `path`, `git`, or `version`.
	//
	// Fix: add at least one source for the dependency.
	CodeManifestBadDepSpec = "E2017"

	// `[workspace]` section declares no members.
	//
	// Fix: add at least one member path under `members = [...]`.
	CodeManifestWorkspaceEmpty = "E2018"

	// Manifest — I/O.

	// `osty.toml` missing from a directory that needs it.
	//
	// Fix: create the file or run the command in a different directory.
	CodeManifestNotFound = "E2030"

	// I/O error reading `osty.toml`.
	//
	// Fix: check file permissions and disk state.
	CodeManifestReadError = "E2031"

	// A workspace member path doesn't exist.
	//
	// Fix: create the directory, or remove the member entry.
	CodeManifestMemberMiss = "E2032"

	// Scaffolding.

	// `osty new NAME` — NAME doesn't satisfy identifier rules.
	//
	// Fix: pick a name starting with a letter, using `[a-z0-9_-]`.
	CodeScaffoldInvalidName = "E2050"

	// Destination directory already exists.
	//
	// The scaffolder never overwrites — it requires a fresh target path.
	//
	// Fix: choose an empty target, or delete / move the existing directory first.
	CodeScaffoldDestExists = "E2051"

	// I/O error creating the new project files.
	//
	// Fix: check write permissions and free space.
	CodeScaffoldWriteError = "E2052"
)

const (
	// Lint — unused declarations.

	// A `let` binding is introduced but never referenced.
	//
	// Example:
	//   fn f() {
	//       let unused = 42   // warning: binding `unused` is never used
	//       println("hi")
	//   }
	// Fix: remove the binding, or rename it to begin with `_` to acknowledge the intentional discard.
	CodeUnusedLet = "L0001"

	// A function or closure parameter is declared but never referenced.
	//
	// Public functions (`pub fn`) are exempt since their parameters are
	// part of the external contract.
	//
	// Example:
	//   fn greet(name: String, times: Int) {   // warning on `times`
	//       println(name)
	//   }
	// Fix: remove the parameter, or rename it to `_times`.
	CodeUnusedParam = "L0002"

	// A `use` alias is introduced but never referenced.
	//
	// Works at package scope: cross-file uses of the alias count.
	//
	// Example:
	//   use foo.bar.baz
	//
	//   fn main() { println("hi") }   // warning: imported `baz` never used
	// Fix: remove the `use`, or prefix the alias with `_` if kept for side effects.
	CodeUnusedImport = "L0003"

	// `let mut x = ...` is declared mutable but never reassigned.
	//
	// Fix: drop the `mut` qualifier.
	CodeUnusedMut = "L0004"

	// A `mut` binding is reassigned without the previous value ever being
	// read — the old write is "dead" and the first assignment is wasted
	// work.
	//
	// Example:
	//   let mut x = heavy()   // warning: value overwritten before use
	//   x = 1
	//   println(x)
	// Fix: remove the initial assignment, or read the old value before overwriting.
	CodeDeadStore = "L0008"

	// Struct field is never read anywhere in the package.
	//
	// Fix: remove the field, or mark it `pub` if it's part of the external contract.
	CodeUnusedField = "L0005"

	// Private method is never called.
	//
	// Fix: remove the method, or make it `pub` if it's intended as public API.
	CodeUnusedMethod = "L0006"

	// `Result` / `Option` value discarded at statement level.
	//
	// Silently dropping a fallible result usually indicates a missed error path.
	//
	// Fix: bind the result (`let _ = ...`), propagate with `?`, or match on it.
	CodeIgnoredResult = "L0007"

	// Lint — shadowing.

	// Inner `let` hides an outer name.
	//
	// Example:
	//   fn f() {
	//       let x = 1
	//       {
	//           let x = 2   // warning: `x` shadows an outer binding
	//           println(x)
	//       }
	//   }
	// Fix: rename the inner binding, or prefix with `_` if the shadow is intentional.
	CodeShadowedBinding = "L0010"

	// Lint — unreachable / dead code.

	// Statement appears after an unconditional terminator.
	//
	// A statement after `return`, `break`, or `continue` at the same
	// block level is unreachable.
	//
	// Example:
	//   fn f() -> Int {
	//       return 1
	//       let dead = 2     // warning: unreachable code
	//       dead
	//   }
	// Fix: remove the unreachable code, or move the terminator.
	CodeDeadCode = "L0020"

	// `else` after an `if` branch that unconditionally returns is
	// redundant — the body below the `if` is only reached when the
	// condition is false.
	//
	// Example:
	//   if c {
	//       return 1
	//   } else {                 // warning: redundant `else`
	//       return 2
	//   }
	// Fix: hoist the `else` body to the top level.
	CodeRedundantElse = "L0021"

	// The `if` condition is a compile-time constant — the branch is
	// either always taken or always skipped.
	//
	// Plain `for { ... }` is an idiomatic infinite loop and is NOT
	// flagged; this rule targets only `if true`, `if false`, `if !true`,
	// `if !false`, and `while`-like for-conditions with the same shape.
	//
	// Example:
	//   if true { do() }    // warning: always-true condition
	// Fix: drop the `if`, or replace with the real condition.
	CodeConstantCondition = "L0022"

	// An `if`/`else` branch is an empty block `{}`. Usually a
	// placeholder the author forgot to fill in — noisy in real programs.
	//
	// Example:
	//   if c {
	//       work()
	//   } else {                 // warning: empty else branch
	//   }
	// Fix: remove the empty branch, or fill it in.
	CodeEmptyBranch = "L0023"

	// A `return x` at the tail position of a function body is
	// unnecessary — the expression alone is already the return value
	// (§6 implicit return).
	//
	// Example:
	//   fn f() -> Int {
	//       return 42   // warning: use the bare expression instead
	//   }
	// Fix: drop the `return` keyword.
	CodeNeedlessReturn = "L0024"

	// Both `if` and `else` branches evaluate to the same expression — the
	// condition is dead code.
	//
	// Example:
	//   let y = if c { 1 } else { 1 }   // warning: both branches identical
	// Fix: replace with the expression directly (dropping `if`).
	CodeIdenticalBranches = "L0025"

	// Loop body is empty.
	//
	// `for x in xs {}` and `for cond {}` with no side-effecting body are
	// almost always a bug, or the loop should be replaced with a call
	// that consumes the iterator.
	//
	// Example:
	//   for x in work() { }   // warning: empty loop body
	// Fix: do something with each item, or drop the loop.
	CodeEmptyLoopBody = "L0026"

	// Lint — naming conventions.

	// Type name is not written in UpperCamelCase.
	//
	// Applies to structs, enums, interfaces, type aliases, and generic parameters.
	//
	// Example:
	//   struct my_struct { x: Int }   // warning: should be `MyStruct`
	// Fix: rename using UpperCamelCase.
	CodeNamingType = "L0030"

	// Function, method, `let`, or parameter name is not written in lowerCamelCase.
	//
	// Example:
	//   fn LoadConfig() { }          // warning: should be `loadConfig`
	//   fn f(User_Id: Int) {}        // warning: should be `userId`
	// Fix: rename using lowerCamelCase.
	CodeNamingValue = "L0031"

	// Enum variant is not written in UpperCamelCase.
	//
	// Example:
	//   enum Color { red, Green }   // warning on `red`
	// Fix: rename using UpperCamelCase.
	CodeNamingVariant = "L0032"

	// Lint — redundant forms.

	// `if c { true } else { false }` collapses to `c`.
	//
	// Fix: replace with the bare condition.
	CodeRedundantBool = "L0040"

	// `x == x` / `x != x` compares a value to itself.
	//
	// Almost always a typo — one side was meant to be a different name.
	//
	// Fix: compare to the intended operand.
	CodeSelfCompare = "L0041"

	// `x = x` assigns a variable to itself.
	//
	// Fix: remove the assignment, or correct one of the operands.
	CodeSelfAssign = "L0042"

	// `!!x` is a no-op on Bool.
	//
	// Example:
	//   if !!ready { ... }   // warning: double negation
	// Fix: drop both `!` operators.
	CodeDoubleNegation = "L0043"

	// `x == true` / `x == false` / `x != true` / `x != false` — comparing
	// a Bool to a Bool literal is redundant.
	//
	// Example:
	//   if done == true { ... }   // warning: drop `== true`
	// Fix: use the Bool directly (`if done`, `if !done`).
	CodeBoolLiteralCompare = "L0044"

	// `!true` / `!false` — negated literal is just the other literal.
	//
	// Example:
	//   let x = !true      // warning: use `false` directly
	// Fix: replace with the opposite literal.
	CodeNegatedBoolLiteral = "L0045"

	// Lint — complexity.

	// A function declares too many parameters (> 7 by default).
	//
	// Long parameter lists are a maintenance hazard and often mean the
	// function should be split or should take a config struct.
	//
	// Fix: group related parameters into a struct, or split the function.
	CodeTooManyParams = "L0050"

	// A function body is too long (> 80 statements by default).
	//
	// Long bodies are hard to review; extract helpers.
	//
	// Fix: factor out cohesive subtasks into helper functions.
	CodeFunctionTooLong = "L0052"

	// Control-flow nesting is too deep (> 5 levels by default).
	//
	// Deeply nested code is hard to follow; early-return or extract
	// helpers.
	//
	// Fix: flatten the structure via early returns / guard clauses, or
	// extract inner branches into helpers.
	CodeDeepNesting = "L0053"

	// Lint — documentation.

	// A `pub` declaration has no doc comment.
	//
	// Public items are the module's external contract — callers benefit
	// from a one-line `///` description.
	//
	// Example:
	//   pub fn hashPassword(p: String) -> String { ... }   // warning: missing doc
	// Fix: add a doc comment, or drop `pub` if the item is internal.
	CodeMissingDoc = "L0070"
)
