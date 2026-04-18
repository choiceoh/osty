# Osty Diagnostic Reference

Every diagnostic the compiler front-end emits carries a stable code.
This document is the authoritative list; when `osty check` produces
an error, searching its code here shows the rule, a minimal
reproduction, and the usual fix.

This file is **generated from `internal/diag/codes.go`**. Edit the
doc comments there, then run `go generate ./internal/diag/...`.

Codes are namespaced by phase:

| Range | Phase |
|---|---|
| `E0001–E0099` | Lexical |
| `E0100–E0199` | Declarations / statements |
| `E0200–E0299` | Expressions |
| `E0300–E0399` | Types / patterns |
| `E0400–E0499` | Annotations |
| `E0500–E0599` | Name resolution |
| `E0600–E0699` | Control flow / context checks |
| `E0700–E0799` | Type checking |
| `W0750` | Deprecation warning |
| `E2000–E2099` | Manifest / scaffolding |
| `L0001–L0099` | Lint warnings (`osty lint`) |

---

## Lexical (E0001–E0009)

### E0001 — `CodeUnterminatedString`

A string literal reaches end-of-file without a closing quote.

```osty
let s = "hello
```

**Fix**: add the closing `"`. For multi-line text use triple-quoted strings.

### E0002 — `CodeUppercaseBasePrefix`

Base prefixes must be lowercase.

Spec: v0.2 R11 / v0.4 §1.6.1

```osty
let n = 0X1F  // rejected
```

**Fix**: use `0x1F` / `0b1010` / `0o777`.

### E0003 — `CodeUnknownEscape`

The escape sequence is unknown or references an invalid Unicode scalar value.

Most commonly a surrogate code point.

Spec: v0.4 §2.1

```osty
let c = '\u{D800}'  // rejected
```

**Fix**: use a non-surrogate scalar (U+0..U+D7FF or U+E000..U+10FFFF).

### E0004 — `CodeUnterminatedComment`

A block comment reaches end-of-file without closing.

```osty
/* never closed
```

**Fix**: close the block with `*/`.

### E0005 — `CodeIllegalCharacter`

A byte that does not begin any valid token.

Commonly non-ASCII input outside of string literals.

**Fix**: remove the stray byte or move it inside a string.

### E0006 — `CodeBadTripleString`

Triple-quoted string violates the indent rules.

The opening `"""` must be followed by a newline, every content line must begin with the closing-line's whitespace prefix, and the closing `"""` must be on its own line.

Spec: v0.4 §1.6.3

**Fix**: realign the content and closing delimiter per §1.6.3.

### E0007 — `CodeFatArrowRemoved`

The `=>` (fat-arrow) token was removed from the grammar.

`match` arms and every other arrow position use `->` instead. Any occurrence of `=>` in source is a lex error (O7, §1.7).

Spec: v0.4 §1.7, OSTY_GRAMMAR_v0.4 O7

```osty
match x { 0 => "zero", _ => "other" }  // rejected
```

**Fix**: replace `=>` with `->`.

### E0008 — `CodeBadNumericSeparator`

A numeric literal places `_` outside the allowed between-digits position.

`_` may only appear between two digits of the same base. Leading underscores after a base prefix, trailing underscores, consecutive underscores, and underscores adjacent to `.` or `e`/`E` are all rejected.

Spec: v0.4 §1.6.1

```osty
let a = 1_            // trailing
let b = 0x_FF         // after base prefix
let c = 1__000        // consecutive
```

**Fix**: place `_` only between two digits.

### E0009 — `CodeBadCharLiteral`

A char or byte literal is empty, holds more than one Unicode scalar, or holds a non-ASCII scalar where only bytes are permitted.

Char literals hold exactly one Unicode scalar; byte literals (`b'...'`) hold exactly one ASCII scalar.

(ASCII only for `b'...'`).

Spec: v0.4 §1.6.4

```osty
let a = ''             // empty
let b = b'\u{1F600}'   // non-ASCII byte
let c = 'ab'           // multiple scalars
```

**Fix**: put exactly one Unicode scalar between the quotes

---

## Declarations & statements (E0100–E0106)

### E0100 — `CodeExpectedDecl`

A token that cannot begin a top-level declaration appeared where one was expected.

**Fix**: precede the token with a valid declaration keyword (`fn`, `let`, `struct`, …).

### E0101 — `CodeUseGoFnHasBody`

Functions declared inside `use go "..."` must not have a body.

They forward to the imported Go function.

Spec: v0.4 R17

```osty
use go "net/http" {
    fn Get(url: String) -> String { "x" }  // rejected
}
```

**Fix**: drop the body — keep only the signature.

### E0102 — `CodeUseGoStructHasMethod`

Structs inside `use go { ... }` blocks mirror Go field layout only.

Methods live on the Go side.

Spec: v0.4 R16

**Fix**: move the method definition to the Go file that owns the type.

### E0103 — `CodeUseGoUnsupported`

A feature not permitted inside a `use go` block.

Generics, parameter defaults, enums, interfaces, type aliases, and bodies on `fn` are all rejected.

**Fix**: simplify the declaration to a bare field layout or signature.

### E0104 — `CodeUsePathMixed`

A `use` path mixes dotted and urlish forms.

A path is either dotted (`std.fs`) OR urlish (`github.com/x/y`) — the two cannot mix.

Spec: v0.4 R15

**Fix**: choose one form for the whole path.

### E0105 — `CodeElseAcrossNewline`

`else` appears on a new line.

It must sit on the same line as the closing `}` of the `if` body.

Spec: v0.4 O2

```osty
if cond {
    ...
}
else {  // rejected
    ...
}
```

**Fix**: move `else` onto the same line as the preceding `}`.

### E0106 — `CodeDefaultExprNotLiteral`

Parameter or field default is not a literal.

Defaults must be restricted literal forms (literal, `-` numeric, `None`, `Ok(lit)`, `Err(lit)`, `[]`, `{:}`, `()`).

Spec: v0.4 R18

```osty
fn connect(t: Int = computeTimeout()) {}  // rejected
```

**Fix**: use a literal default, or move the computation into the body.

---

## Expressions (E0200–E0204)

### E0200 — `CodeNonAssocChain`

Comparison or range operators are non-associative.

Spec: v0.4 R1

```osty
a < b < c      // rejected
0..10..20      // rejected
```

**Fix**: parenthesize — `(a < b) && (b < c)`.

### E0201 — `CodeTurbofishMissingLT`

`::` is reserved for turbofish and must be followed by `<`.

Spec: v0.4 O6

```osty
foo::bar()     // rejected — did you mean `foo.bar()`?
```

**Fix**: use `.` for member access or `::<T>` for type application.

### E0202 — `CodeTrailingDot`

Method chains must continue with a leading dot on the next line.

A trailing `.` then newline is a syntax error.

Spec: v0.4 O3

**Fix**: move the `.` to the start of the continuation line.

### E0203 — `CodeClosureRetReqBlock`

A closure with an explicit return type must have a block body.

Spec: v0.4 R25

```osty
let f = |x: Int| -> Int x * 2       // rejected
let f = |x: Int| -> Int { x * 2 }   // ok
```

**Fix**: wrap the expression in `{ ... }`.

### E0204 — `CodeUnexpectedToken`

Fallback for expression-position tokens that don't begin a valid primary expression.

**Fix**: check for a missing operand, operator, or brace.

---

## Types & patterns (E0300–E0301)

### E0300 — `CodeExpectedType`

A token that cannot begin a type appeared in a type position.

**Fix**: supply a type name, `Self`, or a parenthesized type form.

### E0301 — `CodeExpectedPattern`

A token that cannot begin a pattern appeared in a pattern position.

**Fix**: supply a literal, variant, struct, tuple, or `_` pattern.

---

## Annotations (E0400)

### E0400 — `CodeUnknownAnnotation`

The annotation name is not recognized.

Only `#[json(...)]` and `#[deprecated(...)]` are defined today.

Spec: v0.4 R26

**Fix**: remove the annotation or use one of the recognized names.

---

## Name resolution (E0500–E0509)

### E0500 — `CodeUndefinedName`

The referenced identifier is not in scope.

Typo suggestions use edit distance — the diagnostic says "did you mean `X`?" when a nearby name exists.

**Fix**: import the name, or correct the spelling.

### E0501 — `CodeDuplicateDecl`

The same name is declared twice in the same scope.

Scopes affected: top-level, struct fields, enum variants, methods, or a single block.

**Fix**: rename one of the declarations.

### E0502 — `CodeWrongSymbolKind`

A name is used in a position that disagrees with its declaration.

For example, a function name used where a type is expected.

**Fix**: use a name of the right kind, or adjust the expected position.

### E0503 — `CodeSelfOutsideMethod`

`self` is only valid as the first parameter of a method and inside that method's body.

**Fix**: move the reference inside a method, or rename the identifier.

### E0504 — `CodeSelfTypeOutside`

`Self` is only valid inside a `struct`, `enum`, or `interface` body.

**Fix**: replace with the actual type name outside the declaration.

### E0505 — `CodeUnknownPackage`

The referenced package cannot be found.

Spec: v0.4 §5

**Fix**: check the `use` path and verify the package is on disk or in the manifest.

### E0506 — `CodeCyclicImport`

A `use` graph contains a cycle.

Package A imports B which eventually imports A. The resolver breaks the cycle and reports the first edge that closes it.

Spec: v0.4 §5.3

**Fix**: extract shared declarations into a third package that both sides import.

### E0507 — `CodePrivateAcrossPackages`

A cross-package reference targets a non-`pub` item.

Private items are visible only to other files in the same package.

Spec: v0.4 §5.2

**Fix**: add `pub` to the declaration, or move the caller into the same package.

### E0508 — `CodeUnknownExportedMember`

Package member access names an item that isn't exported.

The member might be private, misspelled, or from a different package.

Spec: v0.4 §5.2

**Fix**: verify the name is `pub` and matches the exported spelling.

### E0509 — `CodeStdlibNotAvailable`

A `use std.*` import cannot be resolved because the stdlib provider is unavailable.

The compiler runs with a lazily-loaded stdlib descriptor; if it hasn't been loaded for the current invocation, `std.*` names fall back to this error.

**Fix**: invoke the compiler with the standard entrypoint that wires up the stdlib.

---

## Control flow / context (E0600–E0609)

### E0600 — `CodeBreakOutsideLoop`

`break` must be inside a `for` loop.

**Fix**: enclose the statement in a `for` body, or remove it.

### E0601 — `CodeContinueOutsideLoop`

`continue` must be inside a `for` loop.

**Fix**: enclose the statement in a `for` body, or remove it.

### E0602 — `CodeReturnOutsideFn`

`return` must be inside a function body.

Scripts count — their top-level statements are wrapped in an implicit `main()`.

**Fix**: move the `return` into a function, or drop it from a library file.

### E0603 — `CodeDeferOutsideFn`

`defer` must be inside a function body.

**Fix**: move the `defer` into a function.

### E0604 — `CodeWildcardInExpr`

`_` is a pattern wildcard; it cannot stand in for a value in an expression.

```osty
let x = _  // rejected
```

**Fix**: for ignored bindings use `let _ = expr`.

### E0605 — `CodeOrPatternBindingMismatch`

Every alternative of an or-pattern must bind the same names.

Spec: v0.4 §4.3.1

```osty
match e {
    A(x) | B(x, y) -> ...   // rejected: `y` not bound by A
}
```

**Fix**: rebalance the alternatives to bind the same names.

### E0606 — `CodeInterfaceDefaultField`

An interface default method may not access fields on `self`.

The interface has no view of the implementing type's layout.

Spec: v0.4 §2.6.2

**Fix**: call other interface methods instead of reading fields directly.

### E0607 — `CodeAnnotationBadTarget`

The annotation's target is not in its permitted set.

`#[json]` only attaches to struct fields; `#[deprecated]` to top-level declarations and methods; neither attaches to `use`.

Spec: v0.4 §18.1

**Fix**: move the annotation to a permitted target.

### E0608 — `CodeDeferAtScriptTop`

Bare `defer` at the top level of a script is rejected.

Spec: v0.4 §6 / §18.3

**Fix**: wrap the cleanup in an explicit `fn` or move it inside an existing function body.

### E0609 — `CodeDuplicateAnnotation`

The same annotation name may not appear twice on a single target.

Spec: v0.4 §18.1

```osty
#[deprecated]
#[deprecated]           // rejected
pub fn f() {}
```

**Fix**: remove the duplicate.

---

## Type checking (E0700–E0753)

### E0700 — `CodeTypeMismatch`

Wrong type in assignment, return, or argument position.

**Fix**: convert or choose a compatible type.

### E0701 — `CodeWrongArgCount`

Call arity mismatch.

**Fix**: pass the expected number of arguments.

### E0702 — `CodeUnknownField`

`foo.bar` — no such field.

**Fix**: check the field name against the struct definition.

### E0703 — `CodeUnknownMethod`

`foo.bar()` — no such method.

**Fix**: verify the method exists on the type or its implemented interfaces.

### E0704 — `CodeNotCallable`

Call target isn't a function, method, or variant.

**Fix**: only functions, methods, and tuple-struct/variant constructors are callable.

### E0705 — `CodeNotIndexable`

`x[i]` — type has no indexing.

**Fix**: switch to a type that supports indexing (list, map, string).

### E0706 — `CodeNotAStruct`

`T { ... }` — `T` isn't a struct.

**Fix**: use a struct type, or construct via the correct factory.

### E0707 — `CodeUnknownStructField`

Struct literal names a field the struct doesn't have.

**Fix**: remove the extra field or correct its name.

### E0708 — `CodeMissingStructField`

Struct literal omits a required field.

**Fix**: add the missing field or give it a default in the declaration.

### E0709 — `CodeVariantShape`

Enum variant payload has the wrong arity or shape.

**Fix**: match the payload signature declared on the variant.

### E0710 — `CodeNotAVariant`

Pattern names something that isn't a variant.

**Fix**: use a real variant of the scrutinee's enum.

### E0711 — `CodeMatchArmMismatch`

Match arms don't unify to a single result type.

**Fix**: coerce arms to a common type or split the match.

### E0712 — `CodeIfBranchMismatch`

`if` / `else` branches don't unify.

**Fix**: give both branches the same type, or use `if` as a statement.

### E0713 — `CodeBinaryOpUntyped`

Operator not defined on the operand types.

**Fix**: convert an operand or use a different operator.

### E0714 — `CodeUnaryOpUntyped`

Unary operator not defined on the operand type.

**Fix**: check that the type supports the operator (e.g. `Bool` for `!`, `Int`/`Float` for `-`).

### E0715 — `CodeConditionNotBool`

`if` / `for` condition isn't `Bool`.

**Fix**: produce a `Bool` from the expression (e.g. `x != 0`).

### E0716 — `CodeNotIterable`

`for x in e` — `e` has no iterator.

**Fix**: iterate over a list, map, range, or `Iterator`-implementing type.

### E0717 — `CodeQuestionNotPropagate`

`?` used on a non-`Result` / non-`Option` value.

**Fix**: only use `?` on fallible types.

### E0718 — `CodeQuestionBadReturn`

`?` used where the enclosing return type cannot hold the propagated error.

**Fix**: change the fn return type to `Result<...>` / `Option<...>`.

### E0719 — `CodeOptionalChainOnNon`

`?.` used on a non-`Option` receiver.

**Fix**: drop the `?` (plain `.`) or wrap the receiver in `Option`.

### E0720 — `CodeCoalesceNonOptional`

`??` left-hand side is not `Option`.

**Fix**: change the LHS to an optional, or replace `??` with another fallback form.

### E0721 — `CodeNumericLitRange`

Numeric literal does not fit in the inferred type.

```osty
let x: UInt8 = 300  // rejected
```

**Fix**: widen the target type or shrink the literal.

### E0722 — `CodeLitPatternMismatch`

Literal pattern type does not match the scrutinee.

**Fix**: use a pattern whose type matches the value.

### E0723 — `CodeRangePatternNonOrd`

Range pattern requires an `Ordered` scrutinee.

**Fix**: switch to an ordered type (numbers, chars) or explode the range.

### E0724 — `CodeAssignTarget`

LHS of `=` is not assignable.

**Fix**: assign into a `let mut` binding, a struct field, or an index.

### E0725 — `CodeMutabilityMismatch`

Assign into a non-`mut` binding, or into a field of a non-`mut` receiver.

**Fix**: add `mut` to the binding, or rebind via `let`.

### E0726 — `CodeReturnTypeMismatch`

Return expression doesn't match the fn signature.

**Fix**: return a value of the declared type, or change the signature.

### E0727 — `CodeGenericArgCount`

Wrong number of type arguments for a generic.

**Fix**: supply exactly as many type args as the generic declares (or omit for inference).

### E0728 — `CodeUnknownVariant`

`Enum.Variant` — `Variant` isn't declared on the enum.

**Fix**: check the variant name against the enum definition.

### E0729 — `CodeTypeNotOrdered`

`<`, `<=`, `>`, `>=` used on a non-`Ordered` type.

**Fix**: only compare types that implement `Ordered`.

### E0730 — `CodeTypeNotEqual`

`==` / `!=` used on a non-`Equal` type.

**Fix**: only compare types that implement `Equal`.

### E0731 — `CodeNonExhaustiveMatch`

Match doesn't cover every case of the scrutinee.

**Fix**: add the missing arms or a catch-all `_ ->` branch.

### E0732 — `CodeKeywordArgUnknown`

Keyword argument names no such parameter.

**Fix**: check the parameter name against the fn signature.

### E0733 — `CodePositionalAfterKw`

Positional argument appears after a keyword argument.

**Fix**: move all positional arguments before the first keyword argument.

### E0734 — `CodeDuplicateArg`

Same parameter passed twice (positionally and by name, or two keyword args).

**Fix**: pass each parameter at most once.

### E0735 — `CodeInterpolationNonStr`

Interpolated expression doesn't implement `ToString`.

**Fix**: call `.toString()` explicitly or wrap in `str(...)`.

### E0736 — `CodeIterableNotProtocol`

`for-in` receiver doesn't implement the `Iterator` protocol.

The resolver accepts any value the checker couldn't disprove, but the checker requires either a built-in iterable or a type that implements `Iterator<Item = T>` / `next()`.

**Fix**: implement `Iterator` on the type, or convert to a known iterable.

### E0737 — `CodeChannelWrongValue`

`ch <- v` where `v`'s type doesn't match the channel element type.

**Fix**: send a value of the channel's `Chan<T>` element type.

### E0738 — `CodeChannelNotChan`

`ch <- v` where `ch` isn't a `Chan<T>`.

**Fix**: use a channel on the left-hand side of `<-`.

### E0739 — `CodeAnnotationBadArg`

Annotation argument has the wrong type.

```osty
#[json(key = 42)]   // `key` expects a String
```

**Fix**: pass an argument whose type matches the annotation's schema.

### E0740 — `CodeUnreachableArm`

Match arm is unreachable because a previous arm fully covers its cases.

**Fix**: merge or remove the shadowed arm.

### E0741 — `CodeRefutablePattern`

Pattern in an irrefutable position can fail to match.

Three spec sites require irrefutable patterns: `let p = e` (§A.5 let bindings), `for p in e` (§A.5 for-in bindings), and closure parameters (G16 — destructured at every call site). Irrefutable means: identifiers, `_`, tuples/structs made only of irrefutable sub-patterns, or `name @ irrefutable`.

Spec: v0.4 §A.5, G16

**Fix**: accept the value with an irrefutable pattern, then use `match` or `if let` inside the body for the refutable cases.

### E0742 — `CodeGenericCallableReference`

Generic function or method is referenced without being called.

Osty v0.4 does not have first-class polymorphic function values; generic callables must be instantiated by a call site, or wrapped in a closure that fixes the type arguments.

Spec: v0.4 G14

**Fix**: call the generic directly, or write a wrapper closure such as `|x| f::<Int>(x)`.

### E0743 — `CodeCapabilityEscape`

Structured-concurrency capability escapes its group scope.

`Handle<T>` and `TaskGroup` are non-escaping capabilities. They may be used in the same `taskGroup` scope, joined/cancelled there, and passed to helpers that do not store or return them. Returning one, storing one in a field/collection, sending one over a channel, or capturing one in an escaping closure is rejected.

Spec: v0.4 G13

**Fix**: join/use the handle inside the `taskGroup` closure and return an ordinary value.

### E0744 — `CodeOperandType`

Operator cannot be applied to the operand's type.

Currently the runtime's catch-all for unary (`!`, `-`, `+`, `~`), binary arithmetic / bitwise / comparison / logical, `??` coalesce, `<-` channel send, and `in` membership type mismatches. The more specialized codes E0713/E0714/E0720/E0737/E0738 are reserved but not currently emitted — callers should expect E0744 today.

**Fix**: convert an operand, or switch to an operator defined on the type.

### E0745 — `CodeUnknownName`

Resolver could not find a name in the current scope.

Emitted by the checker when an identifier reference doesn't match any local binding or top-level function in scope (the resolver passed it through as a last-chance lookup, typically because of missing imports or a typo).

**Fix**: check spelling, imports, and receiver type; if it's a method, write the receiver explicitly.

### E0746 — `CodeDuplicateField`

Struct literal (or declaration) names the same field twice.

**Fix**: remove the duplicate or rename one of the entries.

### E0747 — `CodeDuplicateMethod`

Two methods on the same type share a name.

**Fix**: rename one of the methods, or merge their bodies.

### E0748 — `CodeCannotInferTyParam`

A type parameter could not be inferred from the arguments.

**Fix**: supply the type argument explicitly via turbofish `f::<T>(...)`, or pass an argument whose type constrains the parameter.

### E0749 — `CodeGenericBoundViolation`

A type argument violates a generic bound.

```osty
fn f<T: Ordered>(x: T) { ... }
f("hello")     // String is not Ordered → rejected
```

**Fix**: switch to a type that satisfies the bound, or relax the bound.

### E0751 — `CodeInterfaceNotSatisfied`

A concrete type does not satisfy a required interface.

Osty's interfaces are structural — every method in the interface must be present on the concrete type with a matching signature.

**Fix**: add the missing methods, or switch to a type that already satisfies the interface.

### E0752 — `CodeClosureAnnotationRequired`

Closure parameter lacks a type annotation in a context where it cannot be inferred (no expected-type hint from the call site).

**Fix**: annotate the parameter explicitly (`|x: Int| ...`), or use the closure in a position that provides an expected type.

### E0753 — `CodePatternShapeMismatch`

Pattern structure does not match the scrutinee's type.

Covers literal / range / tuple-arity / struct / variant pattern shape errors — a broader category than the literal-type mismatch E0722: the scrutinee might be an Int where the pattern is a tuple, or the scrutinee a tuple of arity 3 where the pattern is arity 2.

**Fix**: rewrite the pattern to match the scrutinee's shape, or guard with a type-narrowing arm above it.

---

## Deprecation warning (W0750–E0762)

### W0750 — `CodeDeprecatedUse`

Use site references an item marked `#[deprecated]`.

Emitted as a `diag.Warning`. Tooling can promote it to error via build configuration.

Spec: v0.4 §3.8.2

**Fix**: migrate to the replacement noted in the `#[deprecated]` annotation.

### E0760 — `CodeUnreachableCode`

Control flow diagnostics (E0760-E0769).

CodeUnreachableCode: a statement appears after a divergent construct (return, break, continue, or an expression of type Never) and therefore can never execute.

Spec: v0.4 §4 control flow, §2.1 Never

**Fix**: delete the dead statement or move it above the divergent one.

### E0761 — `CodeMissingReturn`

CodeMissingReturn: a non-unit function's body could reach its end without producing a value matching the return type.

function's result.

Spec: v0.4 §3.1

**Fix**: add an explicit `return` or make the final expression the

### E0762 — `CodeDefaultNotLiteral`

CodeDefaultNotLiteral: a default argument expression is not a literal (§3.1 forbids computed defaults).

bool, `None`, `Ok(literal)`, `Err(literal)`, `[]`, `{:}`, or `()` literal.

**Fix**: replace the expression with a numeric, string, char, byte,

---

## Runtime sublanguage (E0771–E0772)

### E0771 — `CodePodShapeViolation`

CodePodShapeViolation: a `struct` carrying `#[pod]` violates the LANG_SPEC §19.4 plain-old-data rule. The diagnostic names the first offending field for non-Pod field types, or the first generic parameter that lacks a `T: Pod` bound for unbounded generic structs.

(primitives, `RawPtr`, other `#[pod] #[repr(c)]` structs, tuples of `Pod`, `Option<T: Pod>`); for unbounded generic structs, add `T: Pod` to every type parameter.

Spec: v0.4 §19.4

**Fix**: replace the offending field's type with a `Pod` type

### E0770 — `CodeRuntimePrivilegeViolation`

CodeRuntimePrivilegeViolation: a runtime-sublanguage surface is used outside a privileged package. The surface includes the annotations `#[intrinsic]`, `#[pod]`, `#[repr(c)]`, `#[export(...)]`, `#[c_abi]`, and `#[no_alloc]`; the opaque type `RawPtr`; the marker trait `Pod`; and any `use std.runtime.*` import. A package is privileged when its fully-qualified path begins with `std.runtime.` or when its manifest declares `[capabilities] runtime = true` and loads from the toolchain workspace root.

workspace packages) add `[capabilities] runtime = true` to the package's `osty.toml`. User code has no way to opt in; refactor it to use ordinary managed types.

Spec: v0.4 §19.2

**Fix**: move the code into `std.runtime.*`, or (for toolchain

### E0772 — `CodeNoAllocViolation`

CodeNoAllocViolation: a function carrying `#[no_alloc]` contains an expression that requires the managed allocator (string interpolation, list/map/set literal, non-Pod struct literal, non-runtime enum construction, Builder use), or calls a function that is not itself `#[no_alloc]`. LANG_SPEC §19 allocates this band at E0770-E0779; the control-flow band E0760-E0769 above is already in use.

(`std.runtime.raw.*`), pre-allocated buffers, plain string literals, or restructure the call so the callee is also `#[no_alloc]`.

Spec: v0.4 §19.6.1

**Fix**: replace the offending expression with raw-memory primitives

---

## Manifest — TOML syntax (E2000–E2004)

### E2000 — `CodeManifestSyntax`

Fallback TOML syntax error in `osty.toml`.

**Fix**: validate the file with a TOML linter; check for quotes and brackets.

### E2001 — `CodeManifestUnterminated`

String or inline table in `osty.toml` is unterminated.

**Fix**: close the quote or table on the same line.

### E2002 — `CodeManifestDuplicateKey`

Same key set twice in the same TOML table.

**Fix**: keep one key; remove or rename the duplicate.

### E2003 — `CodeManifestDuplicateTable`

Same `[table]` header declared twice.

**Fix**: consolidate the two sections into one.

### E2004 — `CodeManifestBadEscape`

Unknown `\escape` in a TOML string.

**Fix**: use a supported escape or a literal string (`'...'`).

---

## Manifest — schema (E2010–E2018)

### E2010 — `CodeManifestMissingPackage`

`osty.toml` has no `[package]` table.

**Fix**: add a `[package]` section with at least `name` and `version`.

### E2011 — `CodeManifestMissingField`

A required field is absent from the manifest.

**Fix**: add the named field under the expected table.

### E2012 — `CodeManifestUnknownKey`

Unrecognized key for a known table.

**Fix**: remove the key or rename it to a documented one.

### E2013 — `CodeManifestFieldType`

Manifest field has the wrong TOML type.

**Fix**: quote strings, wrap arrays in `[]`, and use bare identifiers where required.

### E2014 — `CodeManifestBadName`

`[package]` name does not satisfy identifier rules.

**Fix**: use a lowercase name with letters, digits, and `-` / `_`.

### E2015 — `CodeManifestBadVersion`

`version` is not a valid semver triple.

**Fix**: set `version = "MAJOR.MINOR.PATCH"` (pre-release suffix allowed).

### E2016 — `CodeManifestBadEdition`

`edition` is not a recognized value.

**Fix**: pick a supported edition (e.g. `"2024"`).

### E2017 — `CodeManifestBadDepSpec`

Dependency entry is missing `path`, `git`, or `version`.

**Fix**: add at least one source for the dependency.

### E2018 — `CodeManifestWorkspaceEmpty`

`[workspace]` section declares no members.

**Fix**: add at least one member path under `members = [...]`.

---

## Manifest — I/O (E2030–E2032)

### E2030 — `CodeManifestNotFound`

`osty.toml` missing from a directory that needs it.

**Fix**: create the file or run the command in a different directory.

### E2031 — `CodeManifestReadError`

I/O error reading `osty.toml`.

**Fix**: check file permissions and disk state.

### E2032 — `CodeManifestMemberMiss`

A workspace member path doesn't exist.

**Fix**: create the directory, or remove the member entry.

---

## Scaffolding (E2050–E2052)

### E2050 — `CodeScaffoldInvalidName`

`osty new NAME` — NAME doesn't satisfy identifier rules.

**Fix**: pick a name starting with a letter, using `[a-z0-9_-]`.

### E2051 — `CodeScaffoldDestExists`

Destination directory already exists.

The scaffolder never overwrites — it requires a fresh target path.

**Fix**: choose an empty target, or delete / move the existing directory first.

### E2052 — `CodeScaffoldWriteError`

I/O error creating the new project files.

**Fix**: check write permissions and free space.

---

## Lint — unused declarations (L0001–L0007)

### L0001 — `CodeUnusedLet`

A `let` binding is introduced but never referenced.

```osty
fn f() {
    let unused = 42   // warning: binding `unused` is never used
    println("hi")
}
```

**Fix**: remove the binding, or rename it to begin with `_` to acknowledge the intentional discard.

### L0002 — `CodeUnusedParam`

A function or closure parameter is declared but never referenced.

Public functions (`pub fn`) are exempt since their parameters are part of the external contract.

```osty
fn greet(name: String, times: Int) {   // warning on `times`
    println(name)
}
```

**Fix**: remove the parameter, or rename it to `_times`.

### L0003 — `CodeUnusedImport`

A `use` alias is introduced but never referenced.

Works at package scope: cross-file uses of the alias count.

```osty
use foo.bar.baz

fn main() { println("hi") }   // warning: imported `baz` never used
```

**Fix**: remove the `use`, or prefix the alias with `_` if kept for side effects.

### L0004 — `CodeUnusedMut`

`let mut x = ...` is declared mutable but never reassigned.

**Fix**: drop the `mut` qualifier.

### L0008 — `CodeDeadStore`

A `mut` binding is reassigned without the previous value ever being read — the old write is "dead" and the first assignment is wasted work.

```osty
let mut x = heavy()   // warning: value overwritten before use
x = 1
println(x)
```

**Fix**: remove the initial assignment, or read the old value before overwriting.

### L0005 — `CodeUnusedField`

Struct field is never read anywhere in the package.

**Fix**: remove the field, or mark it `pub` if it's part of the external contract.

### L0006 — `CodeUnusedMethod`

Private method is never called.

**Fix**: remove the method, or make it `pub` if it's intended as public API.

### L0007 — `CodeIgnoredResult`

`Result` / `Option` value discarded at statement level.

Silently dropping a fallible result usually indicates a missed error path.

**Fix**: bind the result (`let _ = ...`), propagate with `?`, or match on it.

---

## Lint — shadowing (L0010)

### L0010 — `CodeShadowedBinding`

Inner `let` hides an outer name.

```osty
fn f() {
    let x = 1
    {
        let x = 2   // warning: `x` shadows an outer binding
        println(x)
    }
}
```

**Fix**: rename the inner binding, or prefix with `_` if the shadow is intentional.

---

## Lint — unreachable / dead code (L0020–L0026)

### L0020 — `CodeDeadCode`

Statement appears after an unconditional terminator.

A statement after `return`, `break`, or `continue` at the same block level is unreachable.

```osty
fn f() -> Int {
    return 1
    let dead = 2     // warning: unreachable code
    dead
}
```

**Fix**: remove the unreachable code, or move the terminator.

### L0021 — `CodeRedundantElse`

`else` after an `if` branch that unconditionally returns is redundant — the body below the `if` is only reached when the condition is false.

```osty
if c {
    return 1
} else {                 // warning: redundant `else`
    return 2
}
```

**Fix**: hoist the `else` body to the top level.

### L0022 — `CodeConstantCondition`

The `if` condition is a compile-time constant — the branch is either always taken or always skipped.

Plain `for { ... }` is an idiomatic infinite loop and is NOT flagged; this rule targets only `if true`, `if false`, `if !true`, `if !false`, and `while`-like for-conditions with the same shape.

```osty
if true { do() }    // warning: always-true condition
```

**Fix**: drop the `if`, or replace with the real condition.

### L0023 — `CodeEmptyBranch`

An `if`/`else` branch is an empty block `{}`. Usually a placeholder the author forgot to fill in — noisy in real programs.

```osty
if c {
    work()
} else {                 // warning: empty else branch
}
```

**Fix**: remove the empty branch, or fill it in.

### L0024 — `CodeNeedlessReturn`

A `return x` at the tail position of a function body is unnecessary — the expression alone is already the return value (§6 implicit return).

```osty
fn f() -> Int {
    return 42   // warning: use the bare expression instead
}
```

**Fix**: drop the `return` keyword.

### L0025 — `CodeIdenticalBranches`

Both `if` and `else` branches evaluate to the same expression — the condition is dead code.

```osty
let y = if c { 1 } else { 1 }   // warning: both branches identical
```

**Fix**: replace with the expression directly (dropping `if`).

### L0026 — `CodeEmptyLoopBody`

Loop body is empty.

`for x in xs {}` and `for cond {}` with no side-effecting body are almost always a bug, or the loop should be replaced with a call that consumes the iterator.

```osty
for x in work() { }   // warning: empty loop body
```

**Fix**: do something with each item, or drop the loop.

---

## Lint — naming conventions (L0030–L0032)

### L0030 — `CodeNamingType`

Type name is not written in UpperCamelCase.

Applies to structs, enums, interfaces, type aliases, and generic parameters.

```osty
struct my_struct { x: Int }   // warning: should be `MyStruct`
```

**Fix**: rename using UpperCamelCase.

### L0031 — `CodeNamingValue`

Function, method, `let`, or parameter name is not written in lowerCamelCase.

```osty
fn LoadConfig() { }          // warning: should be `loadConfig`
fn f(User_Id: Int) {}        // warning: should be `userId`
```

**Fix**: rename using lowerCamelCase.

### L0032 — `CodeNamingVariant`

Enum variant is not written in UpperCamelCase.

```osty
enum Color { red, Green }   // warning on `red`
```

**Fix**: rename using UpperCamelCase.

---

## Lint — redundant forms (L0040–L0045)

### L0040 — `CodeRedundantBool`

`if c { true } else { false }` collapses to `c`.

**Fix**: replace with the bare condition.

### L0041 — `CodeSelfCompare`

`x == x` / `x != x` compares a value to itself.

Almost always a typo — one side was meant to be a different name.

**Fix**: compare to the intended operand.

### L0042 — `CodeSelfAssign`

`x = x` assigns a variable to itself.

**Fix**: remove the assignment, or correct one of the operands.

### L0043 — `CodeDoubleNegation`

`!!x` is a no-op on Bool.

```osty
if !!ready { ... }   // warning: double negation
```

**Fix**: drop both `!` operators.

### L0044 — `CodeBoolLiteralCompare`

`x == true` / `x == false` / `x != true` / `x != false` — comparing a Bool to a Bool literal is redundant.

```osty
if done == true { ... }   // warning: drop `== true`
```

**Fix**: use the Bool directly (`if done`, `if !done`).

### L0045 — `CodeNegatedBoolLiteral`

`!true` / `!false` — negated literal is just the other literal.

```osty
let x = !true      // warning: use `false` directly
```

**Fix**: replace with the opposite literal.

---

## Lint — complexity (L0050–L0053)

### L0050 — `CodeTooManyParams`

A function declares too many parameters (> 7 by default).

Long parameter lists are a maintenance hazard and often mean the function should be split or should take a config struct.

**Fix**: group related parameters into a struct, or split the function.

### L0052 — `CodeFunctionTooLong`

A function body is too long (> 80 statements by default).

Long bodies are hard to review; extract helpers.

**Fix**: factor out cohesive subtasks into helper functions.

### L0053 — `CodeDeepNesting`

Control-flow nesting is too deep (> 5 levels by default).

Deeply nested code is hard to follow; early-return or extract helpers.

extract inner branches into helpers.

**Fix**: flatten the structure via early returns / guard clauses, or

---

## Lint — documentation (L0070)

### L0070 — `CodeMissingDoc`

A `pub` declaration has no doc comment.

Public items are the module's external contract — callers benefit from a one-line `///` description.

```osty
pub fn hashPassword(p: String) -> String { ... }   // warning: missing doc
```

**Fix**: add a doc comment, or drop `pub` if the item is internal.

---

## How codes are assigned

- A new diagnostic gets the next unused code in its phase's range.
- Existing codes never change meaning; if a rule is reformulated the
  diagnostic keeps its code and the message is updated.
- Codes are exported from `internal/diag/codes.go` as `CodeXxx`
  constants so tests and downstream tooling (LSP, docs generator)
  can reference them by name.
