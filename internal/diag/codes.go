package diag

// Stable diagnostic codes. The doc comment on each constant is the
// authoritative copy for ERROR_CODES.md AND the source for the Osty-side
// code→family manifest consumed by toolchain/diagnostic.osty. Regenerate
// both derived artifacts with `go generate ./internal/diag/...` whenever
// you add or edit a code.
//
//go:generate go run ../../cmd/codesdoc -in codes.go -w ../../ERROR_CODES.md
//go:generate go run ../../cmd/codesdoc -in codes.go -manifest ../../toolchain/diag_manifest.osty
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

	// A token that cannot begin a struct member appeared inside a struct body.
	//
	// Struct bodies accept field declarations (`name: Type`) and method
	// declarations (`fn name(...)` / `pub fn ...`). Any other token is a
	// recovery error.
	//
	// Example:
	//   pub struct S {
	//       123,   // rejected -- field or method declaration required
	//   }
	// Fix: provide a field or method declaration.
	CodeExpectedStructMember = "E0107"

	// A token that cannot begin an enum member appeared inside an enum body.
	//
	// Enum bodies accept variant declarations (`Ident(T, U)` / `Ident`)
	// and method declarations. Any other token is a recovery error.
	//
	// Example:
	//   pub enum E {
	//       123,   // rejected
	//   }
	// Fix: provide a variant or method declaration.
	CodeExpectedEnumMember = "E0108"

	// A token that cannot begin an interface member appeared inside an
	// interface body.
	//
	// Interface bodies accept method signatures (`fn name(self) -> T`)
	// and associated type references (identifiers). Any other token is a
	// recovery error.
	//
	// Example:
	//   pub interface I {
	//       123,   // rejected
	//   }
	// Fix: provide a method signature or an associated type name.
	CodeExpectedInterfaceMember = "E0109"

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
	//   foo::bar()     // rejected -- did you mean `foo.bar()`?
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

	// A token that cannot begin a closure parameter appeared between `|...|`.
	//
	// A closure parameter is an identifier, an irrefutable pattern
	// (tuple `(a, b)`, struct `User { name }`, variant `Some(x)`), or
	// `_` for a discarded binding.
	//
	// Example:
	//   let f = |123| x           // rejected
	//   let g = |a, _, (k, v)| v  // ok
	// Fix: use an identifier, `_`, or a destructuring pattern.
	CodeExpectedClosureParam = "E0205"

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

	// CodeBuilderMissingRequiredField: a call to the auto-derived
	// `.build()` on `Type.builder()` did not set every `pub` field
	// that lacks a default. LANG_SPEC §3.3 (G9) makes this a
	// compile-time error and names the missing fields so the fix is
	// a direct chain addition. The diagnostic points at the `.build()`
	// call site because that is where the required-field predicate is
	// evaluated; the `.builder()` root is attached as a supporting
	// span.
	//
	// Spec: v0.5 §3.3, gap G9.
	// Example:
	//
	//   pub struct Point { pub x: Int, pub y: Int }
	//   let p = Point.builder().x(3).build()
	//                                 ^^^^^ missing: y
	//
	// Fix: add `.y(<value>)` to the chain before `.build()`, or set
	// the field at declaration time via a default (`pub y: Int = 0`)
	// to drop it from the required set.
	CodeBuilderMissingRequiredField = "E0774"

	// CodePureViolation: a function carrying `#[pure]` contains a body
	// operation that would make the LLVM `readnone` promise unsound:
	// non-local write, I/O, managed allocation, or a call to a function
	// that is not itself proven `#[pure]`.
	//
	// Spec: v0.6 A13
	// Fix: remove `#[pure]`, prove and mark the callee `#[pure]`, or rewrite the body to local scalar computation.
	CodePureViolation = "E0775"

	// =====================================================================
	// v0.6 — Hidden-Dependency-Surface (G36 – G50)
	// =====================================================================

	// G36 — Capabilities (§20)
	//
	// Capability parameters and `#[ambient]` ergonomics. Capability
	// types: `Clock`, `Rng`, `Env`, `Fs`, `Net`, `Process`, `Console`.

	// CodeAmbientWrongSite: `#[ambient(...)]` is applied to a function
	// that is not at a permitted boundary. Permitted: scripts, `fn main`,
	// `#[test]` / `#[bench]` / `bench*` / `test_*` functions. Library
	// functions must receive capabilities as explicit parameters.
	//
	// Spec: v0.6 §20.3
	// Fix: remove `#[ambient]` and add capability parameters, or move
	//      the function to a permitted boundary.
	CodeAmbientWrongSite = "E0780"

	// CodeAmbientUnknownCapability: `#[ambient(name)]` references a name
	// that is not in the canonical capability set (`clock`, `rng`,
	// `env`, `fs`, `net`, `process`, `console`).
	//
	// Spec: v0.6 §20.6
	// Fix: use one of the recognised capability names. User-defined
	//      capabilities cannot be ambient (`E0789`).
	CodeAmbientUnknownCapability = "E0781"

	// CodeAmbientForwardFailed: a callee at a call site requires a
	// capability whose name does not match any in-scope ambient
	// binding. Auto-forward requires *exact* identifier match.
	//
	// Spec: v0.6 §20.3.1
	// Fix: pass the capability explicitly, or rename either side so
	//      the names match.
	CodeAmbientForwardFailed = "E0782"

	// CodeReproducibleCapabilityNonRepro: an interface marked
	// `#[reproducible_capability]` declares a method that is not itself
	// `#[reproducible]`. The capability cannot be sealed if its surface
	// allows non-deterministic operations.
	//
	// Spec: v0.6 §20.5
	// Fix: add `#[reproducible(scope = "target")]` (or stronger) to
	//      every method, or remove `#[reproducible_capability]`.
	CodeReproducibleCapabilityNonRepro = "E0783"

	// CodeReproducibleViaCapability: a function carrying
	// `#[reproducible]` receives a non-deterministic capability
	// parameter (`Clock`, `Rng`, `Env`, `Fs`, `Net`, `Process`).
	//
	// Spec: v0.6 §20.4
	// Fix: drop the non-det capability parameter, narrow `#[reproducible(
	//      scope = "run")]` if `Console` is the only side-effect, or
	//      remove `#[reproducible]`.
	CodeReproducibleViaCapability = "E0784"

	// CodePureViaCapability: a function carrying `#[pure]` receives any
	// capability parameter. `#[pure]` is the strongest effect annotation
	// and forbids capability flow entirely.
	//
	// Spec: v0.6 §20.4
	// Fix: drop the capability parameter, or use `#[reproducible]` (which
	//      allows deterministic capabilities like `Hash`).
	CodePureViaCapability = "E0785"

	// CodeReproducibleUnorderedIter: a `#[reproducible]` function
	// iterates an unordered collection (`Map.iter`, `Set.iter`) whose
	// element order is implementation-defined.
	//
	// Spec: v0.6 §3.11.3
	// Fix: use `Map.entriesSorted()` / `Set.toListSorted()` for
	//      deterministic ordering.
	CodeReproducibleUnorderedIter = "E0786"

	// CodeReproducibleScopeHierarchy: a `#[reproducible(scope = A)]`
	// function calls a callee with weaker scope. Scope strength:
	// `portable` > `target` > `run`.
	//
	// Spec: v0.6 §3.11.3
	// Fix: strengthen the callee's scope, or weaken the caller's.
	CodeReproducibleScopeHierarchy = "E0787"

	// CodeReproduciblePortableConstraint: a `#[reproducible(scope =
	// "portable")]` function violates one of the cross-platform
	// constraints — endianness-dependent serialisation, `NaN` bit
	// pattern comparison, platform-specific integer width assumption.
	//
	// Spec: v0.6 §3.11.3
	// Fix: use explicit endianness (`bytes.toBigEndian`), avoid raw
	//      NaN comparisons (`isNaN()` is OK), or drop to scope =
	//      "target".
	CodeReproduciblePortableConstraint = "E0788"

	// CodeAmbientUserCapability: `#[ambient(name)]` references a
	// user-defined capability. Ambient binding only supports the
	// stdlib's prelude default instances.
	//
	// Spec: v0.6 §20.3
	// Fix: pass the user capability as an explicit parameter.
	CodeAmbientUserCapability = "E0789"

	// G38 — Spec link (§3.10)

	// CodeSpecAnchorNotFound: `#[spec("§X.Y")]` references a markdown
	// anchor that does not exist in `LANG_SPEC_v0.6/`.
	//
	// Spec: v0.6 §3.10.2
	// Fix: correct the section reference, or add the section to the
	//      spec.
	CodeSpecAnchorNotFound = "E0790"

	// CodeSpecAnchorMoved: `#[spec("§X.Y")]` references a section that
	// has moved to a different chapter. Suggestion includes the new
	// path.
	//
	// Spec: v0.6 §3.10.2
	// Fix: update the reference to the new section path.
	CodeSpecAnchorMoved = "W0790"

	// G46 — Performance contract (§3.15)

	// CodeBudgetStaticViolation: a `#[budget]` static field (`allocs`,
	// `io_calls`, `stack_depth`, `instructions`) is exceeded by the
	// function's compile-time analysis.
	//
	// Spec: v0.6 §3.15.1
	// Fix: reduce allocations / IO / recursion depth, or relax the
	//      budget.
	CodeBudgetStaticViolation = "E0795"

	// CodeBudgetUnknownKey: `#[budget(...)]` uses a key that is not in
	// the recognised set (`allocs`, `io_calls`, `stack_depth`,
	// `instructions`, `time_ms`, `p99_ms`).
	//
	// Spec: v0.6 §3.15
	// Fix: use one of the recognised keys; unknown keys are not a
	//      forward-compatibility hatch.
	CodeBudgetUnknownKey = "E0796"

	// CodeBudgetRuntimeRegression: `osty bench --budget` measured a
	// runtime metric (`time_ms` / `p99_ms`) exceeding the declared
	// `#[budget]`. Emitted as a hard fail in CI mode, warn in
	// interactive mode.
	//
	// Spec: v0.6 §3.15.2
	// Fix: optimise the function, or relax the runtime budget.
	CodeBudgetRuntimeRegression = "W0795"

	// G37 — Information flow (§21)

	// CodeTaintSinkViolation: a value carrying tag `σ` reaches a
	// parameter or position annotated `#[requires("τ")]` where `τ ∉ A`
	// (the value's tag set lacks the required trust tag). The most
	// frequent cause is unsanitised user input flowing into a SQL,
	// shell, path, URL, or HTML sink.
	//
	// Spec: v0.6 §21.5.3 (T-Sink) / §21.8
	// Example:
	//   fn h(form: #[taint("user_input")] String) {
	//       db.query("SELECT * FROM users WHERE id = {form}")  // E0900
	//   }
	// Fix: pass through a sanitiser that produces the required trust
	//      tag (`std.sql.escape` for `sql_safe`, `std.shell.quote` for
	//      `shell_safe`, etc.), or use the parametrised form
	//      (`db.exec("... WHERE id = ?", [form])`).
	CodeTaintSinkViolation = "E0900"

	// CodeTaintUnknownTag: `#[taint("σ")]` uses a tag identifier that
	// has no producer or sanitiser registered. Unknown tags would
	// silently never propagate.
	//
	// Spec: v0.6 §21.2
	// Fix: register the tag via a `#[sanitizes(σ, ...)]` or another
	//      `#[taint(σ)]` source, or rename to a known tag.
	CodeTaintUnknownTag = "E0901"

	// CodeSanitizesUnknownSource: `#[sanitizes("σ", into = "τ")]`
	// references a source tag `σ` that is never produced by any
	// `#[taint(σ)]` annotation. The sanitiser cannot fire.
	//
	// Spec: v0.6 §21.2
	// Fix: add a `#[taint(σ)]` source, or correct the source tag name.
	CodeSanitizesUnknownSource = "E0902"

	// CodeRequiresUnknownTag: `#[requires("τ")]` uses a trust tag `τ`
	// that is never produced by any `#[sanitizes(_, into = τ)]`. The
	// requirement is unsatisfiable.
	//
	// Spec: v0.6 §21.2
	// Fix: add a `#[sanitizes(... into = τ)]` somewhere in the
	//      reachable surface, or correct the tag name.
	CodeRequiresUnknownTag = "E0903"

	// CodeTrustedDeclassifyAudit: a function or position uses
	// `#[trusted_declassify(reason = "...")]` to drop a flow tag
	// without going through a sanitiser. Always emitted (audit hint),
	// never blocks compilation.
	//
	// Spec: v0.6 §21.7
	// Fix: where possible, replace with a real sanitiser. Otherwise
	//      ensure the `reason` text is informative — `osty audit
	//      --trusted-declassify` enumerates all sites.
	CodeTrustedDeclassifyAudit = "W0901"

	// CodeUnsafeSilentMatchCompat: a `#[match_compat("X.Y", unsafe_silent
	// = true)]` is in effect, or a `#[stability("internal")]` API is used
	// from outside the same package. Both produce a warning that surfaces
	// in `osty audit`.
	//
	// Spec: v0.6 §3.14.4 / §3.14.2
	// Fix: provide a `fallback = name` to `#[match_compat]`, or import
	//      from a stable API surface.
	CodeUnsafeSilentMatchCompat = "W0902"

	// =====================================================================
	// v0.6 — Annotation / declaration extensions
	// =====================================================================

	// G50 — Anonymous structural record (§2.5.4)

	// CodeAnonRecordAnnotation: an attempt was made to attach an
	// annotation, method, or `pub` modifier to a field of an
	// `AnonymousRecordType`. Anonymous records are *structural* and
	// metadata-free; promote to a nominal `struct` for these features.
	//
	// Spec: v0.6 §2.5.4.4
	// Fix: declare a nominal `struct` if the field needs annotations.
	CodeAnonRecordAnnotation = "E0340"

	// CodeAnonRecordRecursive: an `AnonymousRecordType` references
	// itself in one of its field types (directly or transitively).
	// Anonymous records cannot be self-recursive — the resulting type
	// would be infinite.
	//
	// Spec: v0.6 §2.5.4.4
	// Fix: declare a nominal `struct` and use it by name in the
	//      recursive position.
	CodeAnonRecordRecursive = "E0341"

	// CodeAnonRecordAmbiguous: a `{ ... }` literal cannot be
	// disambiguated between block expression and anonymous record
	// literal at this position. Most common cause: closure body where
	// `: T` could parse either as record field type or as a statement-
	// level annotation.
	//
	// Spec: v0.6 §2.5.4.6 / R29
	// Fix: add a type ascription (`: { x: Int }`) on the value position
	//      or wrap in `(...)` to force expression context.
	CodeAnonRecordAmbiguous = "E0342"

	// G41 — Error contract (§7.5)

	// CodeErrorContractMismatch: a function carrying `#[error_contract]`
	// returns `Err(V)` where `V` is not in the contract. The contract
	// is the authoritative failure-mode catalogue; deviations break
	// caller match exhaustiveness.
	//
	// Spec: v0.6 §7.5.2
	// Fix: add the variant to the contract, or change the return path
	//      to use a contracted variant.
	CodeErrorContractMismatch = "E0410"

	// CodeErrorContractUnknownVariant: a contract entry references a
	// variant that does not exist on the function's declared error
	// type.
	//
	// Spec: v0.6 §7.5.2
	// Fix: correct the variant name, or extend the error enum.
	CodeErrorContractUnknownVariant = "E0411"

	// CodeErrorContractDeadVariant: a contract variant is declared but
	// never produced by any return path.
	//
	// Spec: v0.6 §7.5.2
	// Fix: remove the variant from the contract, or add a code path
	//      that returns it.
	CodeErrorContractDeadVariant = "W0411"

	// CodeErrorContractOnErased: `#[error_contract]` is applied to a
	// function whose error type is the erased `Error` interface.
	// Contracts only have meaning over concrete enum types. The
	// declarative form `#[error_contract(any)]` is permitted (no check).
	//
	// Spec: v0.6 §7.5.5
	// Fix: change the error type to a concrete enum, or use
	//      `#[error_contract(any)]` for documentation only.
	CodeErrorContractOnErased = "E0412"

	// CodeMatchExcludesContractVariant: a `match` arm references an
	// `Err(V)` where `V` is on the callee's enum but not in the
	// callee's `#[error_contract]`. The arm is dead code per the
	// contract.
	//
	// Spec: v0.6 §7.5.7
	// Fix: drop the arm, or extend the callee's contract to include V.
	CodeMatchExcludesContractVariant = "W0413"

	// G40 — Sealed construct (§3.4.5)

	// CodeSealedExternalLiteral: a `struct` carrying
	// `#[sealed_construct(name)]` was instantiated via struct literal
	// outside the named constructor or other authorised paths.
	//
	// Spec: v0.6 §3.4.5.2
	// Fix: route construction through the sealed constructor (e.g.,
	//      `Type.parse(...)`), or remove the seal if direct
	//      construction is acceptable.
	CodeSealedExternalLiteral = "E0420"

	// CodeTestConstructInProduction: a function carrying
	// `#[test_construct]` is reachable from a non-test build.
	// The annotation only relaxes sealed-construct rules in the test
	// profile.
	//
	// Spec: v0.6 §3.4.5.5
	// Fix: ensure the function is only called from `#[test]` or
	//      `#[cfg(test)]` paths, or drop `#[test_construct]`.
	CodeTestConstructInProduction = "E0421"

	// CodeTrustedConstructInUserPackage: `#[trusted_construct(...)]`
	// is applied in a user package. The annotation is reserved for
	// stdlib / internal packages where sealed constructors cannot
	// cover every necessary path (e.g., binary deserialisers).
	//
	// Spec: v0.6 §3.4.5.6
	// Fix: route through the public sealed constructor, or move the
	//      code into `std.*` if it genuinely needs the bypass.
	CodeTrustedConstructInUserPackage = "E0422"

	// CodeSealedConstructorNotMethod: `#[sealed_construct(name)]` names
	// an identifier that is not a method or associated function on the
	// struct.
	//
	// Spec: v0.6 §3.4.5
	// Fix: name an existing constructor method, or define one.
	CodeSealedConstructorNotMethod = "E0423"

	// G42 — Structured intent (§3.12)

	// CodeExampleMismatch: an `#[example(input = ..., output = ...)]`
	// invocation produced an output that differs from the expected
	// value.
	//
	// Spec: v0.6 §3.12.2
	// Fix: correct the input/output, or fix the function body.
	CodeExampleMismatch = "E0430"

	// CodeExampleArityMismatch: `#[example(input = [...])]` provides a
	// number of arguments that does not match the function's arity
	// (capabilities counted as one-each).
	//
	// Spec: v0.6 §3.12.2
	// Fix: adjust the input list to match the function's positional
	//      arity.
	CodeExampleArityMismatch = "E0431"

	// CodeFixtureNonZeroArity: `#[fixture(name = "...")]` is applied
	// to a function that takes parameters. Fixtures must be zero-arity
	// to be sharable across docs / tests / context / property seed.
	//
	// Spec: v0.6 §3.12.3
	// Fix: remove the parameters (use closures or inline state if
	//      needed), or drop `#[fixture]`.
	CodeFixtureNonZeroArity = "E0432"

	// CodeExampleUnknownFixture: `#[example(uses = "name")]` references
	// a fixture name that has no `#[fixture(name = "name")]` declared.
	//
	// Spec: v0.6 §3.12.3
	// Fix: declare the fixture, or correct the name.
	CodeExampleUnknownFixture = "E0433"

	// G43 — Executable spec block (§3.13)

	// CodeSpecBlockMisplaced: a `spec { ... }` block appears outside
	// the function-body first-statement position.
	//
	// Spec: v0.6 §3.13 / R28
	// Fix: move the block to the start of the function body.
	CodeSpecBlockMisplaced = "E0440"

	// CodeSpecBlockExampleNotBool: a `spec { example: expr }` clause's
	// `expr` does not evaluate to `Bool`.
	//
	// Spec: v0.6 §3.13.2
	// Fix: rewrite the example as a boolean comparison
	//      (`fn(args) == expected`).
	CodeSpecBlockExampleNotBool = "E0441"

	// CodeSpecBlockLawNotBool: a `spec { law: expr }` or `spec {
	// invariant: expr }` clause's `expr` does not evaluate to `Bool`.
	// Permitted free identifiers inside `law` / `invariant` include
	// `result` (the function's return value, virtual binding).
	//
	// Spec: v0.6 §3.13.5
	// Fix: rewrite as a boolean expression.
	CodeSpecBlockLawNotBool = "E0442"

	// CodeSpecBlockForallBadGenerator: a `spec { forall x in expr: ... }`
	// clause's `expr` is not of type `Gen<T>` for some `T`. (Phase 5 / v1
	// only — earlier phases reject `forall` syntactically.)
	//
	// Spec: v0.6 §3.13.3
	// Fix: use a `std.testing.gen.*` constructor.
	CodeSpecBlockForallBadGenerator = "E0443"

	// G45 — Golden tests (§11.5)

	// CodeGoldenNotReproducible: a function carrying `#[golden]` does
	// not satisfy `#[reproducible(scope = "target")]`. Non-deterministic
	// snapshots have no diagnostic value.
	//
	// Spec: v0.6 §11.5.4
	// Fix: ensure the function (and its callees) are reproducible —
	//      pass capabilities explicitly and avoid time/random/env access.
	CodeGoldenNotReproducible = "E0444"

	// CodeGoldenSnapshotMissing: a `#[golden(path)]` declaration's
	// snapshot file does not exist on disk. On first run, `osty test
	// --update-golden` creates it.
	//
	// Spec: v0.6 §11.5.2
	// Fix: run `osty test --update-golden` to create the snapshot,
	//      then audit its contents.
	CodeGoldenSnapshotMissing = "E0445"

	// CodeGoldenAstParseError: `#[golden(mode = "ast")]` is applied
	// to a function whose output is not valid Osty source — reparse
	// failed.
	//
	// Spec: v0.6 §11.5.3
	// Fix: use `mode = "text"` (byte-exact) or `mode = "json"` if the
	//      output is structured but not Osty source.
	CodeGoldenAstParseError = "E0446"

	// CodeGoldenSnapshotStale: `#[golden]` snapshot's `source-hash`
	// header does not match the current function definition. The
	// snapshot may be stale.
	//
	// Spec: v0.6 §11.5.4
	// Fix: review the snapshot for correctness, then `osty test
	//      --update-golden` to refresh.
	CodeGoldenSnapshotStale = "W0444"

	// G44 — API evolution (§3.14)

	// CodeMatchCompatNoFallback: `#[match_compat("X.Y")]` is applied
	// without specifying `fallback = name` or `unsafe_silent = true`.
	// Silent fallthrough is forbidden by default.
	//
	// Spec: v0.6 §3.14.4
	// Fix: add `fallback = handlerName`, or explicitly opt into
	//      `unsafe_silent = true` (which always emits W0902).
	CodeMatchCompatNoFallback = "E0450"

	// CodeMatchCompatUnknownVersion: `#[match_compat("X.Y")]` references
	// a version that the compiler does not recognise (newer than the
	// running compiler, or never released).
	//
	// Spec: v0.6 §3.14.4
	// Fix: use a known version, or update the compiler.
	CodeMatchCompatUnknownVersion = "E0451"

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

	// G44 — Publishing (§3.14.3)

	// CodePublishStableBreaking: `osty publish` detected a breaking
	// change in a `#[stability("stable")]` API surface item but the new
	// version number does not bump the major component. SemVer
	// compatibility is enforced by the registry.
	//
	// Spec: v0.6 §3.14.3.2 / §3.14.3.3
	// Fix: bump the major version, or revert the breaking change. The
	//      diagnostic enumerates each breaking item.
	CodePublishStableBreaking = "E2100"

	// CodePublishVersionDowngrade: `osty publish` rejects a manifest
	// whose version is lower than the previously published version.
	//
	// Spec: v0.6 §3.14.3.3
	// Fix: bump the version forward.
	CodePublishVersionDowngrade = "E2101"

	// CodePublishCompatAddPatchOnly: `osty publish` detected new public
	// API surface items but the version bump is patch-only. Additive
	// changes require at least a minor bump per SemVer.
	//
	// Spec: v0.6 §3.14.3.3
	// Fix: bump the minor version, or remove the new items.
	CodePublishCompatAddPatchOnly = "E2102"

	// CodePublishExperimentalChange: `osty publish` detected a change
	// to a `#[stability("experimental")]` API surface item. Experimental
	// APIs are exempt from SemVer enforcement, but the change is logged
	// for audit.
	//
	// Spec: v0.6 §3.14.3.2
	// Fix: review whether the API is ready to be promoted to "stable".
	CodePublishExperimentalChange = "W2100"
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

	// A function declared `-> Result<T, E>` or `-> Option<T>` whose body
	// only ever exits via `Ok(...)` or `Some(...)` — the wrapping is
	// pure noise at every call site.
	//
	// Example:
	//   fn parse(s: String) -> Result<Int, Error> {
	//       Ok(s.len())              // warning: fn never returns Err
	//   }
	// Fix: drop the wrapping and declare the plain return type:
	//   fn parse(s: String) -> Int { s.len() }
	CodeUnnecessaryWrap = "L0046"

	// `let x = expr; x` at the tail of a block is a useless round-trip.
	// The binding is introduced and immediately returned with no other
	// uses — the block can just be `expr`.
	//
	// Example:
	//   fn double(n: Int) -> Int {
	//       let out = n * 2     // warning: useless binding before tail return
	//       out
	//   }
	// Fix: drop the let and return the expression directly.
	CodeLetReturnSimplify = "L0047"

	// Parentheses wrapping an `if` / `for` / `match` condition are
	// pure noise — they add nothing syntactic and clippy-style
	// convention keeps conditions bare.
	//
	// Example:
	//   if (ready) { ... }        // warning: drop the parens
	//   for (i in 0..n) { ... }   // warning: drop the parens
	// Fix: unwrap the outer `(` / `)`.
	CodeNeedlessParens = "L0048"

	// `for true { ... }` is just `for { ... }` — an infinite loop
	// whose literal condition adds nothing.
	//
	// Example:
	//   for true { tick() }   // warning: redundant `true`
	// Fix: drop the `true`.
	CodeInfiniteLoopLiteral = "L0049"

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

	// A `test_*` function has no `testing.*` call in its body — the
	// test silently passes no matter what the code under test does.
	// Almost certainly a scaffolding leftover or a typo in an
	// assertion helper name.
	//
	// Example:
	//   fn test_parsesEmpty() {
	//       let r = parse("")          // warning: no testing assertion
	//   }
	// Fix: add a `testing.assertEq` / `testing.assert` / `testing.fail`
	// call, or rename the function so it isn't auto-discovered.
	CodeMissingTestAssertion = "L0080"
)
