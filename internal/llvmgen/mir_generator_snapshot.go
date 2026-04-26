// mir_generator_snapshot.go snapshots the Osty-authored MIR→LLVM emitter
// helpers into the native backend package so the hand-written
// mir_generator.go shrinks section-by-section as the self-host port lands.
// Source of truth: toolchain/mir_generator.osty. Post #854 the Osty→Go
// transpiler is retired, so this file is now a hand-maintained mirror —
// edit the Osty source first, then update the matching Go declarations
// here. The `// Osty: toolchain/mir_generator.osty:L:C` comments anchor
// each Go entity to its Osty origin.

package llvmgen

import (
	"math"
	"strconv"
	llvmStrings "strings"
)

// Osty: toolchain/mir_generator.osty:32:5
func mirLlvmTypeForPrim(name string) string {
	// Osty: toolchain/mir_generator.osty:33:5
	if name == "Int" || name == "Int64" || name == "UInt64" {
		// Osty: toolchain/mir_generator.osty:34:9
		return "i64"
	}
	// Osty: toolchain/mir_generator.osty:36:5
	if name == "Int32" || name == "UInt32" || name == "Char" {
		// Osty: toolchain/mir_generator.osty:37:9
		return "i32"
	}
	// Osty: toolchain/mir_generator.osty:39:5
	if name == "Int16" || name == "UInt16" {
		// Osty: toolchain/mir_generator.osty:40:9
		return "i16"
	}
	// Osty: toolchain/mir_generator.osty:42:5
	if name == "Int8" || name == "UInt8" || name == "Byte" {
		// Osty: toolchain/mir_generator.osty:43:9
		return "i8"
	}
	// Osty: toolchain/mir_generator.osty:45:5
	if name == "Bool" {
		// Osty: toolchain/mir_generator.osty:46:9
		return "i1"
	}
	// Osty: toolchain/mir_generator.osty:48:5
	if name == "Float" || name == "Float64" {
		// Osty: toolchain/mir_generator.osty:49:9
		return "double"
	}
	// Osty: toolchain/mir_generator.osty:51:5
	if name == "Float32" {
		// Osty: toolchain/mir_generator.osty:52:9
		return "float"
	}
	// Osty: toolchain/mir_generator.osty:55:5
	if name == "String" || name == "Bytes" {
		// Osty: toolchain/mir_generator.osty:56:9
		return "ptr"
	}
	// Osty: toolchain/mir_generator.osty:59:5
	if name == "RawPtr" {
		// Osty: toolchain/mir_generator.osty:60:9
		return "ptr"
	}
	// Osty: toolchain/mir_generator.osty:63:5
	if name == "Unit" || name == "()" {
		// Osty: toolchain/mir_generator.osty:64:9
		return "void"
	}
	// Osty: toolchain/mir_generator.osty:66:5
	if name == "Never" {
		// Osty: toolchain/mir_generator.osty:67:9
		return "void"
	}
	return ""
}

// Osty: toolchain/mir_generator.osty:76:5
func mirLlvmTypeForOpaqueNamed(name string) string {
	// Osty: toolchain/mir_generator.osty:79:5
	if name == "List" || name == "Map" || name == "Set" || name == "Bytes" {
		// Osty: toolchain/mir_generator.osty:80:9
		return "ptr"
	}
	// Osty: toolchain/mir_generator.osty:83:5
	if name == "Channel" || name == "Handle" || name == "Group" || name == "TaskGroup" || name == "Select" || name == "Duration" {
		// Osty: toolchain/mir_generator.osty:85:9
		return "ptr"
	}
	// Osty: toolchain/mir_generator.osty:89:5
	if name == "ClosureEnv" {
		// Osty: toolchain/mir_generator.osty:90:9
		return "ptr"
	}
	return ""
}

// Osty: toolchain/mir_generator.osty:104:5
func mirTupleTagForPrim(name string) string {
	// Osty: toolchain/mir_generator.osty:105:5
	if name == "Int" || name == "Int64" || name == "UInt64" {
		// Osty: toolchain/mir_generator.osty:106:9
		return "i64"
	}
	// Osty: toolchain/mir_generator.osty:108:5
	if name == "Int32" || name == "UInt32" || name == "Char" {
		// Osty: toolchain/mir_generator.osty:109:9
		return "i32"
	}
	// Osty: toolchain/mir_generator.osty:111:5
	if name == "Int16" || name == "UInt16" {
		// Osty: toolchain/mir_generator.osty:112:9
		return "i16"
	}
	// Osty: toolchain/mir_generator.osty:114:5
	if name == "Int8" || name == "UInt8" || name == "Byte" {
		// Osty: toolchain/mir_generator.osty:115:9
		return "i8"
	}
	// Osty: toolchain/mir_generator.osty:117:5
	if name == "Bool" {
		// Osty: toolchain/mir_generator.osty:118:9
		return "i1"
	}
	// Osty: toolchain/mir_generator.osty:120:5
	if name == "Float" || name == "Float64" {
		// Osty: toolchain/mir_generator.osty:121:9
		return "f64"
	}
	// Osty: toolchain/mir_generator.osty:123:5
	if name == "Float32" {
		// Osty: toolchain/mir_generator.osty:124:9
		return "f32"
	}
	// Osty: toolchain/mir_generator.osty:126:5
	if name == "String" {
		// Osty: toolchain/mir_generator.osty:127:9
		return "string"
	}
	// Osty: toolchain/mir_generator.osty:129:5
	if name == "Bytes" {
		// Osty: toolchain/mir_generator.osty:130:9
		return "bytes"
	}
	// Osty: toolchain/mir_generator.osty:135:5
	if name == "Unit" || name == "()" {
		// Osty: toolchain/mir_generator.osty:136:9
		return "unit"
	}
	return ""
}

// Osty: toolchain/mir_generator.osty:147:5
func mirTupleTagForNamed(name string, builtin bool) string {
	// Osty: toolchain/mir_generator.osty:151:5
	if builtin {
		// Osty: toolchain/mir_generator.osty:152:9
		if name == "List" || name == "Map" || name == "Set" || name == "Bytes" || name == "ClosureEnv" {
			// Osty: toolchain/mir_generator.osty:154:13
			return "ptr"
		}
	}
	// Osty: toolchain/mir_generator.osty:160:5
	if name == "Channel" || name == "Handle" || name == "Group" || name == "TaskGroup" || name == "Select" || name == "Duration" {
		// Osty: toolchain/mir_generator.osty:162:9
		return "ptr"
	}
	return name
}

// Osty: toolchain/mir_generator.osty:173:5
func mirOptionalTypeName(innerTag string) string {
	return "Option." + innerTag
}

// Osty: toolchain/mir_generator.osty:182:5
func mirOptionTypeName(innerTag string) string {
	// Osty: toolchain/mir_generator.osty:183:5
	if innerTag == "" {
		// Osty: toolchain/mir_generator.osty:184:9
		return "Option"
	}
	return "Option." + innerTag
}

// Osty: toolchain/mir_generator.osty:193:5
func mirResultTypeName(okTag string, errTag string) string {
	// Osty: toolchain/mir_generator.osty:194:5
	if okTag == "" {
		// Osty: toolchain/mir_generator.osty:195:9
		return "Result"
	}
	// Osty: toolchain/mir_generator.osty:197:5
	if errTag == "" {
		// Osty: toolchain/mir_generator.osty:198:9
		return "Result." + okTag
	}
	return "Result." + okTag + "." + errTag
}

// Osty: toolchain/mir_generator.osty:214:5
func mirTupleTypeNameFromTags(tags []string) string {
	// Osty: toolchain/mir_generator.osty:215:5
	joined := ""
	_ = joined
	// Osty: toolchain/mir_generator.osty:216:5
	first := true
	_ = first
	// Osty: toolchain/mir_generator.osty:217:5
	for _, tag := range tags {
		// Osty: toolchain/mir_generator.osty:218:9
		if first {
			// Osty: toolchain/mir_generator.osty:219:13
			joined = tag
			// Osty: toolchain/mir_generator.osty:220:13
			first = false
		} else {
			// Osty: toolchain/mir_generator.osty:222:13
			joined = joined + "." + tag
		}
	}
	return "Tuple." + joined
}

// Osty: toolchain/mir_generator.osty:236:5
func mirBinaryOpcode(symbol string, isFloat bool) string {
	// Osty: toolchain/mir_generator.osty:237:5
	if symbol == "+" {
		// Osty: toolchain/mir_generator.osty:238:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:238:22
			return "fadd"
		}
		// Osty: toolchain/mir_generator.osty:239:9
		return "add"
	}
	// Osty: toolchain/mir_generator.osty:241:5
	if symbol == "-" {
		// Osty: toolchain/mir_generator.osty:242:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:242:22
			return "fsub"
		}
		// Osty: toolchain/mir_generator.osty:243:9
		return "sub"
	}
	// Osty: toolchain/mir_generator.osty:245:5
	if symbol == "*" {
		// Osty: toolchain/mir_generator.osty:246:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:246:22
			return "fmul"
		}
		// Osty: toolchain/mir_generator.osty:247:9
		return "mul"
	}
	// Osty: toolchain/mir_generator.osty:249:5
	if symbol == "/" {
		// Osty: toolchain/mir_generator.osty:250:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:250:22
			return "fdiv"
		}
		// Osty: toolchain/mir_generator.osty:251:9
		return "sdiv"
	}
	// Osty: toolchain/mir_generator.osty:253:5
	if symbol == "%" {
		// Osty: toolchain/mir_generator.osty:254:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:254:22
			return "frem"
		}
		// Osty: toolchain/mir_generator.osty:255:9
		return "srem"
	}
	// Osty: toolchain/mir_generator.osty:257:5
	if symbol == "==" {
		// Osty: toolchain/mir_generator.osty:258:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:258:22
			return "fcmp oeq"
		}
		// Osty: toolchain/mir_generator.osty:259:9
		return "icmp eq"
	}
	// Osty: toolchain/mir_generator.osty:261:5
	if symbol == "!=" {
		// Osty: toolchain/mir_generator.osty:262:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:262:22
			return "fcmp one"
		}
		// Osty: toolchain/mir_generator.osty:263:9
		return "icmp ne"
	}
	// Osty: toolchain/mir_generator.osty:265:5
	if symbol == "<" {
		// Osty: toolchain/mir_generator.osty:266:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:266:22
			return "fcmp olt"
		}
		// Osty: toolchain/mir_generator.osty:267:9
		return "icmp slt"
	}
	// Osty: toolchain/mir_generator.osty:269:5
	if symbol == "<=" {
		// Osty: toolchain/mir_generator.osty:270:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:270:22
			return "fcmp ole"
		}
		// Osty: toolchain/mir_generator.osty:271:9
		return "icmp sle"
	}
	// Osty: toolchain/mir_generator.osty:273:5
	if symbol == ">" {
		// Osty: toolchain/mir_generator.osty:274:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:274:22
			return "fcmp ogt"
		}
		// Osty: toolchain/mir_generator.osty:275:9
		return "icmp sgt"
	}
	// Osty: toolchain/mir_generator.osty:277:5
	if symbol == ">=" {
		// Osty: toolchain/mir_generator.osty:278:9
		if isFloat {
			// Osty: toolchain/mir_generator.osty:278:22
			return "fcmp oge"
		}
		// Osty: toolchain/mir_generator.osty:279:9
		return "icmp sge"
	}
	// Osty: toolchain/mir_generator.osty:284:5
	if symbol == "&&" || symbol == "&" {
		// Osty: toolchain/mir_generator.osty:285:9
		return "and"
	}
	// Osty: toolchain/mir_generator.osty:287:5
	if symbol == "||" || symbol == "|" {
		// Osty: toolchain/mir_generator.osty:288:9
		return "or"
	}
	// Osty: toolchain/mir_generator.osty:290:5
	if symbol == "^" {
		// Osty: toolchain/mir_generator.osty:291:9
		return "xor"
	}
	// Osty: toolchain/mir_generator.osty:293:5
	if symbol == "<<" {
		// Osty: toolchain/mir_generator.osty:294:9
		return "shl"
	}
	// Osty: toolchain/mir_generator.osty:296:5
	if symbol == ">>" {
		// Osty: toolchain/mir_generator.osty:297:9
		return "ashr"
	}
	return ""
}

// Osty: toolchain/mir_generator.osty:308:5
func mirBinaryForcesI1Type(symbol string) bool {
	return symbol == "&&" || symbol == "||"
}

// Osty: toolchain/mir_generator.osty:307:5
func mirUnaryIsIdentity(symbol string) bool {
	return symbol == "+"
}

// Osty: toolchain/mir_generator.osty:323:5
func mirUnaryInstruction(symbol string, argReg string, llvmTy string, isFloat bool) string {
	if symbol == "-" {
		if isFloat {
			return "fneg " + llvmTy + " " + argReg
		}
		return "sub " + llvmTy + " 0, " + argReg
	}
	if symbol == "!" {
		return "xor i1 " + argReg + ", 1"
	}
	if symbol == "~" {
		return "xor " + llvmTy + " " + argReg + ", -1"
	}
	return ""
}

// Osty: toolchain/mir_generator.osty:318:5
func mirLoopHintsActive(vectorizeHint bool, parallelHint bool, unrollHint bool) bool {
	return vectorizeHint || parallelHint || unrollHint
}

// Osty: toolchain/mir_generator.osty:326:5
func mirLoopMDVectorizeEnable() string {
	return "!{!\"llvm.loop.vectorize.enable\", i1 true}"
}

// Osty: toolchain/mir_generator.osty:333:5
func mirLoopMDVectorizeScalable() string {
	return "!{!\"llvm.loop.vectorize.scalable.enable\", i1 true}"
}

// Osty: toolchain/mir_generator.osty:340:5
func mirLoopMDVectorizePredicate() string {
	return "!{!\"llvm.loop.vectorize.predicate.enable\", i1 true}"
}

// Osty: toolchain/mir_generator.osty:349:5
func mirLoopMDUnrollEnable() string {
	return "!{!\"llvm.loop.unroll.enable\", i1 true}"
}

// Osty: toolchain/mir_generator.osty:359:5
func mirLoopMDVectorizeWidth(widthDigits string) string {
	return "!{!\"llvm.loop.vectorize.width\", i32 " + widthDigits + "}"
}

// Osty: toolchain/mir_generator.osty:367:5
func mirLoopMDUnrollCount(countDigits string) string {
	return "!{!\"llvm.loop.unroll.count\", i32 " + countDigits + "}"
}

// Osty: toolchain/mir_generator.osty:375:5
func mirLoopMDParallelAccesses(accessGroupRef string) string {
	return "!{!\"llvm.loop.parallel_accesses\", " + accessGroupRef + "}"
}

// Osty: toolchain/mir_generator.osty:354:5
func mirStringPoolLine(sym string, sizeDigits string, encoded string) string {
	return sym + " = private unnamed_addr constant [" + sizeDigits + " x i8] c\"" + encoded + "\"\n"
}

// Osty: toolchain/mir_generator.osty:362:5
func mirAliasScopeDomainLine(ref string) string {
	return ref + " = distinct !{!\"osty.list.metadata.domain\"}"
}

// Osty: toolchain/mir_generator.osty:369:5
func mirAliasScopeScopeLine(ref string, domainRef string) string {
	return ref + " = distinct !{!\"osty.list.metadata.scope\", " + domainRef + "}"
}

// Osty: toolchain/mir_generator.osty:377:5
func mirAliasScopeListLine(ref string, scopeRef string) string {
	return ref + " = !{" + scopeRef + "}"
}

// Osty: toolchain/mir_generator.osty:385:5
func mirAccessGroupLine(ref string) string {
	return ref + " = distinct !{}"
}

// Osty: toolchain/mir_generator.osty:393:5
func mirFormatFnAttrs(inlineMode int, hot bool, cold bool, pureFn bool, targetFeatures []string) string {
	// Osty: toolchain/mir_generator.osty:400:5
	var parts []string = make([]string, 0, 1)
	_ = parts
	// Osty: toolchain/mir_generator.osty:401:5
	if inlineMode == 1 {
		// Osty: toolchain/mir_generator.osty:402:9
		func() struct{} { parts = append(parts, "inlinehint"); return struct{}{} }()
	} else if inlineMode == 2 {
		// Osty: toolchain/mir_generator.osty:404:9
		func() struct{} { parts = append(parts, "alwaysinline"); return struct{}{} }()
	} else if inlineMode == 3 {
		// Osty: toolchain/mir_generator.osty:406:9
		func() struct{} { parts = append(parts, "noinline"); return struct{}{} }()
	}
	// Osty: toolchain/mir_generator.osty:408:5
	if hot {
		// Osty: toolchain/mir_generator.osty:409:9
		func() struct{} { parts = append(parts, "hot"); return struct{}{} }()
	}
	// Osty: toolchain/mir_generator.osty:411:5
	if cold {
		// Osty: toolchain/mir_generator.osty:412:9
		func() struct{} { parts = append(parts, "cold"); return struct{}{} }()
	}
	// Osty: toolchain/mir_generator.osty:416:5
	if pureFn {
		// Osty: toolchain/mir_generator.osty:417:9
		func() struct{} { parts = append(parts, "readnone"); return struct{}{} }()
	}
	// Osty: toolchain/mir_generator.osty:419:5
	if !(len(targetFeatures) == 0) {
		// Osty: toolchain/mir_generator.osty:423:9
		joined := ""
		_ = joined
		// Osty: toolchain/mir_generator.osty:424:9
		first := true
		_ = first
		// Osty: toolchain/mir_generator.osty:425:9
		for _, f := range targetFeatures {
			// Osty: toolchain/mir_generator.osty:426:13
			stripped := func() string {
				if llvmStrings.HasPrefix(f, "+") {
					return f[1:len(f)]
				} else {
					return f
				}
			}()
			_ = stripped
			// Osty: toolchain/mir_generator.osty:431:13
			if first {
				// Osty: toolchain/mir_generator.osty:432:17
				joined = "+" + stripped
				// Osty: toolchain/mir_generator.osty:433:17
				first = false
			} else {
				// Osty: toolchain/mir_generator.osty:435:17
				joined = joined + ",+" + stripped
			}
		}
		// Osty: toolchain/mir_generator.osty:438:9
		func() struct{} { parts = append(parts, "\"target-features\"=\""+joined+"\""); return struct{}{} }()
	}
	// Osty: toolchain/mir_generator.osty:442:5
	out := ""
	_ = out
	// Osty: toolchain/mir_generator.osty:443:5
	first := true
	_ = first
	// Osty: toolchain/mir_generator.osty:444:5
	for _, p := range parts {
		// Osty: toolchain/mir_generator.osty:445:9
		if first {
			// Osty: toolchain/mir_generator.osty:446:13
			out = p
			// Osty: toolchain/mir_generator.osty:447:13
			first = false
		} else {
			// Osty: toolchain/mir_generator.osty:449:13
			out = out + " " + p
		}
	}
	return out
}

// Osty: toolchain/mir_generator.osty:461:5
func mirLlvmTypeHeadName(typeText string) string {
	// Osty: toolchain/mir_generator.osty:465:5
	ltIdx := llvmStrings.Index(typeText, "<")
	_ = ltIdx
	// Osty: toolchain/mir_generator.osty:466:5
	stripped := func() string {
		if ltIdx >= 0 {
			return typeText[0:ltIdx]
		} else {
			return typeText
		}
	}()
	_ = stripped
	// Osty: toolchain/mir_generator.osty:471:5
	dotIdx := llvmStrings.Index(stripped, ".")
	_ = dotIdx
	return func() string {
		if dotIdx >= 0 {
			return stripped[(dotIdx + 1):len(stripped)]
		} else {
			return stripped
		}
	}()
}

// Osty: toolchain/mir_generator.osty:485:5
func mirLlvmTypeIsOptionalSurface(typeText string) bool {
	// Osty: toolchain/mir_generator.osty:486:5
	n := len(typeText)
	_ = n
	// Osty: toolchain/mir_generator.osty:487:5
	if n == 0 {
		// Osty: toolchain/mir_generator.osty:488:9
		return false
	}
	// Osty: toolchain/mir_generator.osty:490:5
	if !llvmStrings.HasSuffix(typeText, "?") {
		// Osty: toolchain/mir_generator.osty:491:9
		return false
	}
	// Osty: toolchain/mir_generator.osty:495:5
	depth := 0
	_ = depth
	// Osty: toolchain/mir_generator.osty:496:5
	i := 0
	_ = i
	// Osty: toolchain/mir_generator.osty:497:5
	prefix := typeText[0:(n - 1)]
	_ = prefix
	// Osty: toolchain/mir_generator.osty:498:5
	plen := len(prefix)
	_ = plen
	// Osty: toolchain/mir_generator.osty:499:5
	for i < plen {
		// Osty: toolchain/mir_generator.osty:500:9
		ch := prefix[i:(i + 1)]
		_ = ch
		// Osty: toolchain/mir_generator.osty:501:9
		if ch == "<" || ch == "(" {
			// Osty: toolchain/mir_generator.osty:502:13
			func() {
				var _cur1 int = depth
				var _rhs2 int = 1
				if _rhs2 > 0 && _cur1 > math.MaxInt-_rhs2 {
					panic("integer overflow")
				}
				if _rhs2 < 0 && _cur1 < math.MinInt-_rhs2 {
					panic("integer overflow")
				}
				depth = _cur1 + _rhs2
			}()
		} else if ch == ">" || ch == ")" {
			// Osty: toolchain/mir_generator.osty:504:13
			func() {
				var _cur3 int = depth
				var _rhs4 int = 1
				if _rhs4 < 0 && _cur3 > math.MaxInt+_rhs4 {
					panic("integer overflow")
				}
				if _rhs4 > 0 && _cur3 < math.MinInt+_rhs4 {
					panic("integer overflow")
				}
				depth = _cur3 - _rhs4
			}()
		}
		// Osty: toolchain/mir_generator.osty:506:9
		func() {
			var _cur5 int = i
			var _rhs6 int = 1
			if _rhs6 > 0 && _cur5 > math.MaxInt-_rhs6 {
				panic("integer overflow")
			}
			if _rhs6 < 0 && _cur5 < math.MinInt-_rhs6 {
				panic("integer overflow")
			}
			i = _cur5 + _rhs6
		}()
	}
	return depth == 0
}

// Osty: toolchain/mir_generator.osty:521:5
func mirIsHeapEqualityType(typeText string) bool {
	return typeText == "String" || typeText == "Bytes"
}

// Osty: toolchain/mir_generator.osty:531:5
func mirIsStringPrimTypeText(typeText string) bool {
	return typeText == "String"
}

// Osty: toolchain/mir_generator.osty:542:5
func mirIsStringOrderingSymbol(symbol string) bool {
	return symbol == "<" || symbol == "<=" || symbol == ">" || symbol == ">="
}

// Osty: toolchain/mir_generator.osty:552:5
func mirStringOrderingPredicate(symbol string) string {
	// Osty: toolchain/mir_generator.osty:553:5
	if symbol == "<" {
		// Osty: toolchain/mir_generator.osty:554:9
		return "slt"
	}
	// Osty: toolchain/mir_generator.osty:556:5
	if symbol == "<=" {
		// Osty: toolchain/mir_generator.osty:557:9
		return "sle"
	}
	// Osty: toolchain/mir_generator.osty:559:5
	if symbol == ">" {
		// Osty: toolchain/mir_generator.osty:560:9
		return "sgt"
	}
	// Osty: toolchain/mir_generator.osty:562:5
	if symbol == ">=" {
		// Osty: toolchain/mir_generator.osty:563:9
		return "sge"
	}
	return ""
}

// Osty: toolchain/mir_generator.osty:575:5
func mirIsUnitTypeText(typeText string) bool {
	return typeText == "Unit" || typeText == "()" || typeText == "Never"
}

// Osty: toolchain/mir_generator.osty:585:5
func mirIsFloatTypeText(typeText string) bool {
	return typeText == "Float" || typeText == "Float32" || typeText == "Float64" || typeText == "double" || typeText == "float"
}

// Osty: toolchain/mir_generator.osty:596:5
func mirIsScalarLLVMType(t string) bool {
	return t == "i1" || t == "i8" || t == "i16" || t == "i32" || t == "i64" || t == "float" || t == "double" || t == "ptr"
}

// Osty: toolchain/mir_generator.osty:606:5
func mirLlvmI1Text(v bool) string {
	// Osty: toolchain/mir_generator.osty:607:5
	if v {
		// Osty: toolchain/mir_generator.osty:608:9
		return "true"
	}
	return "false"
}

// Osty: toolchain/mir_generator.osty:619:5
func mirFirstNonEmpty(vals []string) string {
	// Osty: toolchain/mir_generator.osty:620:5
	n := len(vals)
	_ = n
	// Osty: toolchain/mir_generator.osty:621:5
	i := 0
	_ = i
	// Osty: toolchain/mir_generator.osty:622:5
	for i < n {
		// Osty: toolchain/mir_generator.osty:623:9
		x := vals[i]
		_ = x
		// Osty: toolchain/mir_generator.osty:624:9
		if x != "" {
			// Osty: toolchain/mir_generator.osty:625:13
			return x
		}
		// Osty: toolchain/mir_generator.osty:627:9
		func() {
			var _cur7 int = i
			var _rhs8 int = 1
			if _rhs8 > 0 && _cur7 > math.MaxInt-_rhs8 {
				panic("integer overflow")
			}
			if _rhs8 < 0 && _cur7 < math.MinInt-_rhs8 {
				panic("integer overflow")
			}
			i = _cur7 + _rhs8
		}()
	}
	return ""
}

// Osty: toolchain/mir_generator.osty:639:5
func mirEarliestAfter(input string, needle string) int {
	return llvmStrings.Index(input, needle)
}

// Osty: toolchain/mir_generator.osty:664:5
func mirEncodeLLVMString(s string) string {
	// Osty: toolchain/mir_generator.osty:674:5
	printable := " !#$%&'()*+,-./0123456789:;<=" + ">?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[]^_`abcdefghijklmnopqrstuvwxyz{|}~"
	_ = printable
	// Osty: toolchain/mir_generator.osty:675:5
	out := ""
	_ = out
	// Osty: toolchain/mir_generator.osty:676:5
	n := len(s)
	_ = n
	// Osty: toolchain/mir_generator.osty:677:5
	i := 0
	_ = i
	// Osty: toolchain/mir_generator.osty:678:5
	for i < n {
		// Osty: toolchain/mir_generator.osty:679:9
		ch := s[i:(i + 1)]
		_ = ch
		// Osty: toolchain/mir_generator.osty:680:9
		if ch == "\\" {
			// Osty: toolchain/mir_generator.osty:681:13
			out = out + "\\\\"
		} else if ch == "\"" {
			// Osty: toolchain/mir_generator.osty:683:13
			out = out + "\\22"
		} else if llvmStrings.Contains(printable, ch) {
			// Osty: toolchain/mir_generator.osty:685:13
			out = out + ch
		} else {
			// Osty: toolchain/mir_generator.osty:690:13
			plen := len(printable)
			_ = plen
			// Osty: toolchain/mir_generator.osty:691:13
			trap := printable[(plen + 1):(plen + 2)]
			_ = trap
			// Osty: toolchain/mir_generator.osty:692:13
			return trap
		}
		// Osty: toolchain/mir_generator.osty:694:9
		func() {
			var _cur9 int = i
			var _rhs10 int = 1
			if _rhs10 > 0 && _cur9 > math.MaxInt-_rhs10 {
				panic("integer overflow")
			}
			if _rhs10 < 0 && _cur9 < math.MinInt-_rhs10 {
				panic("integer overflow")
			}
			i = _cur9 + _rhs10
		}()
	}
	return out + "\\00"
}

// Osty: toolchain/mir_generator.osty:781:5
func mirLlvmGlobalVarLine(name string, llvmType string) string {
	return "@" + name + " = global " + llvmType + " zeroinitializer\n"
}

// Osty: toolchain/mir_generator.osty:789:5
func mirLlvmIfaceTypeDefLine() string {
	return "%osty.iface = type { ptr, ptr }\n"
}

// Osty: toolchain/mir_generator.osty:796:5
func mirLlvmStructTypeDefLine(name string, fieldsJoined string) string {
	return "%" + name + " = type { " + fieldsJoined + " }\n"
}

// Osty: toolchain/mir_generator.osty:806:5
func mirLlvmEnumLayoutTypeDefLine(name string) string {
	return "%" + name + " = type { i64, i64 }\n"
}

// Osty: toolchain/mir_generator.osty:815:5
func mirLlvmVtableDeclLine(symbol string) string {
	return symbol + " = external constant [0 x ptr]\n"
}

// Osty: toolchain/mir_generator.osty:823:5
func mirGlobalCtorsRegistration() string {
	return "@llvm.global_ctors = appending global [1 x { i32, ptr, ptr }] [{ i32, ptr, ptr } { i32 65535, ptr @__osty_init_globals, ptr null }]\n\n"
}

// Osty: toolchain/mir_generator.osty:831:5
func mirInitGlobalsCtorHeader() string {
	return "define private void @__osty_init_globals() {\nentry:\n"
}

// Osty: toolchain/mir_generator.osty:838:5
func mirInitGlobalsCtorFooter() string {
	return "  ret void\n}\n\n"
}

// Osty: toolchain/mir_generator.osty:848:5
func mirInitGlobalsCtorStoreSequence(globName string, retLLVM string, initName string) string {
	tmp := "%v" + globName
	return "  " + tmp + " = call " + retLLVM + " @" + initName + "()\n" +
		"  store " + retLLVM + " " + tmp + ", ptr @" + globName + "\n"
}

// Osty: toolchain/mir_generator.osty:858:5
func mirRuntimeDeclareLine(retTy string, sym string, argList string) string {
	return "declare " + retTy + " @" + sym + "(" + argList + ")"
}

// Osty: toolchain/mir_generator.osty:868:5
func mirRuntimeDeclareMemoryRead(retTy string, sym string, argList string) string {
	return "declare " + retTy + " @" + sym + "(" + argList + ") nounwind willreturn memory(read)"
}

// Osty: toolchain/mir_generator.osty:876:5
func mirRuntimeDeclareNoReturn(retTy string, sym string, argList string, cold bool) string {
	prefix := "declare " + retTy + " @" + sym + "(" + argList + ") noreturn"
	if cold {
		return prefix + " cold nounwind"
	}
	return prefix
}

// Osty: toolchain/mir_generator.osty:879:5
func mirGenIntToString(n int) string {
	// Match the Osty source's algorithm exactly so the byte-for-byte
	// emission stays stable; strconv.Itoa would also work but keeping
	// the manual digit-walk shape parallels the Osty side and lets the
	// next port (a generated mirror) use this signature unchanged.
	return strconv.Itoa(n)
}

// Osty: toolchain/mir_generator.osty:911:5
func mirGenDigitChar(d int) string {
	// Helper retained for parity with the Osty source even though the
	// Go mirror of mirGenIntToString uses strconv.Itoa directly. Keeps
	// the per-helper inventory aligned across the two sides.
	if d >= 0 && d <= 9 {
		return strconv.Itoa(d)
	}
	return "?"
}

// MirSeq mirrors `toolchain/mir_generator.osty:932 MirSeq`. The
// pointer-receiver methods below are the Go counterparts of the
// `mut self` Osty methods — calling `mirGen.seq.Fresh()` consumes
// the current counter and bumps it for the next caller. Phase B
// state package: `TempSeq` (SSA / label numbering), `LoopMDDefs`
// (module-scoped metadata accumulator), `ListMetaScopeList` (cached
// `!alias.scope` ref). Future moves (fnBuf, declares, …) attach to
// this struct as the Osty mirror grows.
type MirSeq struct {
	TempSeq           int
	LoopMDDefs        []string
	ListMetaScopeList string
	// FnBuf is the per-function body accumulator. Mirrors the Osty
	// MirSeq.fnBuf field added in the §15 stateful slice. Populated by
	// AppendFnLine / AbsorbOstyEmitter; drained by FlushFnBuf at
	// function-emission boundaries.
	FnBuf []string
}

// MirLoopHints mirrors
// `toolchain/mir_generator.osty: MirLoopHints`. Plain data — fed
// into `MirSeq.NextLoopMD` so the per-function flag values flow
// in explicitly rather than living on `MirSeq` itself.
type MirLoopHints struct {
	Vectorize              bool
	VectorizeWidth         int
	VectorizeScalable      bool
	VectorizePredicate     bool
	Parallel               bool
	ParallelAccessGroupRef string
	Unroll                 bool
	UnrollCount            int
}

// Osty: toolchain/mir_generator.osty:945:9 (MirSeq.fresh)
func (s *MirSeq) Fresh() string {
	name := "%t" + strconv.Itoa(s.TempSeq)
	s.TempSeq++
	return name
}

// Osty: toolchain/mir_generator.osty:954:9 (MirSeq.freshLabel)
func (s *MirSeq) FreshLabel(prefix string) string {
	name := prefix + "." + strconv.Itoa(s.TempSeq)
	s.TempSeq++
	return name
}

// Osty: toolchain/mir_generator.osty:961:9 (MirSeq.reset)
func (s *MirSeq) Reset() {
	s.TempSeq = 0
}

// Osty: MirSeq.reserveMDRef
func (s *MirSeq) ReserveMDRef() string {
	return "!" + strconv.Itoa(len(s.LoopMDDefs))
}

// Osty: MirSeq.commitMDLine
func (s *MirSeq) CommitMDLine(line string) {
	s.LoopMDDefs = append(s.LoopMDDefs, line)
}

// Osty: MirSeq.allocMDNode
func (s *MirSeq) AllocMDNode(body string) string {
	ref := s.ReserveMDRef()
	s.CommitMDLine(ref + " = " + body)
	return ref
}

// Osty: MirSeq.nextLoopMD
func (s *MirSeq) NextLoopMD(hints MirLoopHints) string {
	var propRefs []string
	if hints.Vectorize {
		propRefs = append(propRefs, s.AllocMDNode(mirLoopMDVectorizeEnable()))
		if hints.VectorizeWidth > 0 {
			propRefs = append(propRefs, s.AllocMDNode(
				mirLoopMDVectorizeWidth(strconv.Itoa(hints.VectorizeWidth))))
		}
		if hints.VectorizeScalable {
			propRefs = append(propRefs, s.AllocMDNode(mirLoopMDVectorizeScalable()))
		}
		if hints.VectorizePredicate {
			propRefs = append(propRefs, s.AllocMDNode(mirLoopMDVectorizePredicate()))
		}
	}
	if hints.Parallel && hints.ParallelAccessGroupRef != "" {
		propRefs = append(propRefs, s.AllocMDNode(
			mirLoopMDParallelAccesses(hints.ParallelAccessGroupRef)))
	}
	if hints.Unroll {
		if hints.UnrollCount > 0 {
			propRefs = append(propRefs, s.AllocMDNode(
				mirLoopMDUnrollCount(strconv.Itoa(hints.UnrollCount))))
		} else {
			propRefs = append(propRefs, s.AllocMDNode(mirLoopMDUnrollEnable()))
		}
	}
	if len(propRefs) == 0 {
		return ""
	}
	loopRef := s.ReserveMDRef()
	children := loopRef
	for _, ref := range propRefs {
		children += ", " + ref
	}
	s.CommitMDLine(loopRef + " = distinct !{" + children + "}")
	return loopRef
}

// Osty: MirSeq.listAliasScopeRef
func (s *MirSeq) ListAliasScopeRef() string {
	if s.ListMetaScopeList != "" {
		return s.ListMetaScopeList
	}
	domainRef := s.ReserveMDRef()
	s.CommitMDLine(mirAliasScopeDomainLine(domainRef))
	scopeRef := s.ReserveMDRef()
	s.CommitMDLine(mirAliasScopeScopeLine(scopeRef, domainRef))
	listRef := s.ReserveMDRef()
	s.CommitMDLine(mirAliasScopeListLine(listRef, scopeRef))
	s.ListMetaScopeList = listRef
	return listRef
}

// Osty: MirSeq.nextAccessGroupMD
func (s *MirSeq) NextAccessGroupMD() string {
	ref := s.ReserveMDRef()
	s.CommitMDLine(mirAccessGroupLine(ref))
	return ref
}

// Osty: toolchain/mir_generator.osty (mirChanRecvSuffix)
func mirChanRecvSuffix(elemLLVM string) string {
	return llvmChanElementSuffix(elemLLVM)
}

// Osty: toolchain/mir_generator.osty (mirMapValueSizeBytes)
func mirMapValueSizeBytes(llvmTyp string) int {
	if llvmTyp == "i64" || llvmTyp == "double" || llvmTyp == "ptr" {
		return 8
	}
	if llvmTyp == "i32" {
		return 4
	}
	if llvmTyp == "i8" || llvmTyp == "i1" {
		return 1
	}
	return 0
}

// Osty: toolchain/mir_generator.osty (mirIntLLVMBits)
func mirIntLLVMBits(t string) int {
	if t == "i1" {
		return 1
	}
	if t == "i8" {
		return 8
	}
	if t == "i16" {
		return 16
	}
	if t == "i32" {
		return 32
	}
	if t == "i64" {
		return 64
	}
	return 0
}

// Osty: toolchain/mir_generator.osty (mirThunkName)
func mirThunkName(symbol string) string {
	return "__osty_closure_thunk_" + symbol
}

// Osty: toolchain/mir_generator.osty (mirIsMemoryAccessLine)
func mirIsMemoryAccessLine(line string) bool {
	n := len(line)
	i := 0
	for i < n && line[i:i+1] == " " {
		i++
	}
	trimmed := line[i:n]
	if llvmStrings.HasPrefix(trimmed, "store ") {
		return true
	}
	return llvmStrings.Contains(line, " = load ")
}

// Osty: toolchain/mir_generator.osty (mirTagParallelAccesses)
func mirTagParallelAccesses(body string, groupRef string) string {
	suffix := ", !llvm.access.group " + groupRef
	n := len(body)
	out := ""
	start := 0
	i := 0
	for i < n {
		if body[i:i+1] == "\n" {
			line := body[start:i]
			if mirIsMemoryAccessLine(line) && !llvmStrings.Contains(line, "!llvm.access.group") {
				out += line + suffix + "\n"
			} else {
				out += body[start : i+1]
			}
			start = i + 1
		}
		i++
	}
	if start < n {
		line := body[start:n]
		if mirIsMemoryAccessLine(line) && !llvmStrings.Contains(line, "!llvm.access.group") {
			out += line + suffix
		} else {
			out += line
		}
	}
	return out
}

// Osty: toolchain/mir_generator.osty (mirEmitHeaderBlock)
func mirEmitHeaderBlock(source string, target string) string {
	out := "; Code generated by osty LLVM MIR backend. DO NOT EDIT.\n"
	out += "; Osty: " + source + "\n"
	out += "source_filename = \"" + source + "\"\n"
	if target != "" {
		out += "target triple = \"" + target + "\"\n"
	}
	return out + "\n"
}

// Osty: toolchain/mir_generator.osty (mirEarliestAfterAny)
func mirEarliestAfterAny(input string, needles []string) int {
	best := -1
	for _, needle := range needles {
		idx := llvmStrings.Index(input, needle)
		if idx >= 0 {
			if best < 0 || idx < best {
				best = idx
			}
		}
	}
	return best
}

// Osty: toolchain/mir_generator.osty (mirInjectBeforeFirstFn)
func mirInjectBeforeFirstFn(body string, block string) string {
	markers := []string{"define ", "declare "}
	idx := mirEarliestAfterAny(body, markers)
	if idx < 0 {
		return body + block
	}
	return body[0:idx] + block + body[idx:]
}

// Osty: toolchain/mir_generator.osty (mirJoinDeclareLines)
func mirJoinDeclareLines(orderedDecls []string) string {
	out := ""
	for _, decl := range orderedDecls {
		out += decl + "\n"
	}
	return out
}

// MirInlineStringEqResult mirrors the Osty struct of the same name.
// Osty: toolchain/mir_generator.osty (MirInlineStringEqResult)
type MirInlineStringEqResult struct {
	FinalReg string
	Lines    []string
}

// EmitInlineStringEqLiteral mirrors MirSeq.emitInlineStringEqLiteral —
// builds the SSO-aware string-equality switch the legacy emitter
// inlined directly into g.fnBuf. Layout:
//
//  1. Pointer-equality fast path against the literal symbol.
//  2. SSO tag check (bit 63 of the dynamic operand pointer). If set,
//     the operand is an inline-tagged string with content packed in
//     the pointer bits — see `osty_rt_string_pack_inline` in the
//     runtime. For literals of length ≤ 7 the operand bits are
//     compared directly to a compile-time constant packed encoding;
//     longer literals can never match an inline operand and shortcut
//     to false.
//  3. Heap path: byte-by-byte compare with NUL terminator (the
//     pre-SSO body, unchanged).
//
// `litBytes` is the per-byte int view of the literal (caller converts
// via int(lit[i])). The != path appends one final `xor i1 ..., true`
// so callers always receive the post-negation register without
// tracking the op themselves.
//
// Implementation parity: every FreshLabel / Fresh call here mirrors
// the Osty source (toolchain/mir_generator.osty:
// emitInlineStringEqLiteral) one-for-one so MirSeq.TempSeq advances
// by exactly the same amount on both paths.
//
// Osty: MirSeq.emitInlineStringEqLiteral
func (s *MirSeq) EmitInlineStringEqLiteral(
	opIsEq bool,
	dynReg string,
	litSym string,
	litBytes []int,
) MirInlineStringEqResult {
	var lines []string
	matchLabel := s.FreshLabel("streq.match")
	nomatchLabel := s.FreshLabel("streq.nomatch")
	doneLabel := s.FreshLabel("streq.done")
	tagCheckLabel := s.FreshLabel("streq.tag")
	heapLabel := s.FreshLabel("streq.heap")

	// (1) Pointer-equality fast path.
	ptrEq := s.Fresh()
	lines = append(lines, mirICmpEqLine(ptrEq, "ptr", dynReg, litSym))
	lines = append(lines, mirBrCondLine(ptrEq, matchLabel, tagCheckLabel))

	// (2) SSO tag check on the dynamic operand. Bit 63 of the pointer
	// is the runtime's small-string tag (always 0 for valid user-space
	// addresses on every supported 64-bit platform).
	lines = append(lines, mirLabelLine(tagCheckLabel))
	rawInt := s.Fresh()
	lines = append(lines, mirPtrToIntLine(rawInt, dynReg, "i64"))
	tagBit := s.Fresh()
	// 1<<63 = -9223372036854775808 in i64 signed canonical form.
	lines = append(lines, mirAndI64Line(tagBit, rawInt, "-9223372036854775808"))
	isInline := s.Fresh()
	lines = append(lines, mirICmpLine(isInline, "ne", "i64", tagBit, "0"))

	n := len(litBytes)
	if n <= 7 {
		// Inline operand can match this literal — compare the packed
		// encoding bit-equal against a compile-time constant.
		inlineLabel := s.FreshLabel("streq.inline")
		lines = append(lines, mirBrCondLine(isInline, inlineLabel, heapLabel))
		lines = append(lines, mirLabelLine(inlineLabel))
		// packed = TAG_BIT | (len << 56) | byte0<<0 | ... | byte_{n-1}<<((n-1)*8)
		var packed uint64 = uint64(1) << 63
		packed |= uint64(n&0x7) << 56
		for i := 0; i < n; i++ {
			packed |= uint64(uint8(litBytes[i])) << uint(i*8)
		}
		inlineEq := s.Fresh()
		// Render as signed i64 (LLVM canonical for negative values).
		lines = append(lines, mirICmpEqLine(inlineEq, "i64", rawInt, strconv.FormatInt(int64(packed), 10)))
		lines = append(lines, mirBrCondLine(inlineEq, matchLabel, nomatchLabel))
	} else {
		// Inline length capped at 7; tagged operand can't match.
		lines = append(lines, mirBrCondLine(isInline, nomatchLabel, heapLabel))
	}

	// (3) Heap path: byte-by-byte with NUL terminator.
	byteLabels := make([]string, 0, n+1)
	for k := 0; k <= n; k++ {
		byteLabels = append(byteLabels, s.FreshLabel("streq.b"+strconv.Itoa(k)))
	}
	lines = append(lines, mirLabelHeadWithBranch(heapLabel, byteLabels[0]))

	for i := 0; i <= n; i++ {
		lines = append(lines, mirLabelLine(byteLabels[i]))
		ptrReg := dynReg
		if i > 0 {
			ptrReg = s.Fresh()
			lines = append(lines, mirGEPInboundsI8Line(ptrReg, dynReg, strconv.Itoa(i)))
		}
		byteReg := s.Fresh()
		lines = append(lines, mirLoadLine(byteReg, "i8", ptrReg))

		expected := 0
		if i < n {
			expected = litBytes[i]
		}
		matchReg := s.Fresh()
		lines = append(lines, mirICmpEqLine(matchReg, "i8", byteReg, strconv.Itoa(expected)))

		nextLabel := matchLabel
		if i < n {
			nextLabel = byteLabels[i+1]
		}
		lines = append(lines, mirBrCondLine(matchReg, nextLabel, nomatchLabel))
	}

	// Joinpoint + i1 phi.
	lines = append(lines, mirLabelHeadWithBranch(matchLabel, doneLabel))
	lines = append(lines, mirLabelHeadWithBranch(nomatchLabel, doneLabel))
	lines = append(lines, mirLabelLine(doneLabel))

	eq := s.Fresh()
	lines = append(lines, mirPhiI1FromTwoLine(eq, matchLabel, nomatchLabel))

	if opIsEq {
		return MirInlineStringEqResult{FinalReg: eq, Lines: lines}
	}
	neq := s.Fresh()
	lines = append(lines, mirXorI1NegLine(neq, eq))
	return MirInlineStringEqResult{FinalReg: neq, Lines: lines}
}

// AppendFnLine pushes a fully-formed line (including any leading
// indent and trailing newline) onto MirSeq.FnBuf — the per-function
// body accumulator. Mirrors `g.fnBuf.WriteString(line)` semantics on
// the legacy Go path. Phase B foundation: future Osty-side state-
// bearing emit methods push their lines here so callers don't have
// to thread a Go strings.Builder through the call.
//
// Osty: MirSeq.appendFnLine
func (s *MirSeq) AppendFnLine(line string) {
	s.FnBuf = append(s.FnBuf, line)
}

// FlushFnBuf returns the accumulated function-body lines and clears
// the buffer in one move. Caller drains into `g.fnBuf` so the
// existing flush-to-`g.out` path stays unchanged.
//
// Osty: MirSeq.flushFnBuf
func (s *MirSeq) FlushFnBuf() []string {
	drained := s.FnBuf
	s.FnBuf = nil
	return drained
}

// AbsorbOstyEmitter syncs a Go-driven LlvmEmitter scope back into
// MirSeq. Bumps TempSeq to the emitter's final value and drains
// `em.body` into FnBuf — matches `func (g *mirGen) flushOstyEmitter`
// byte-for-byte (modulo the destination buffer).
//
// Osty: MirSeq.absorbOstyEmitter
func (s *MirSeq) AbsorbOstyEmitter(em *LlvmEmitter) {
	s.TempSeq = em.temp
	s.FnBuf = append(s.FnBuf, em.body...)
}

// OstyEmitter constructs a fresh LlvmEmitter seeded from the current
// TempSeq. The Go bridge `func (g *mirGen) ostyEmitter` now delegates
// here so the seeding logic lives in one place. The Go LlvmEmitter
// has fields the Osty source struct doesn't model (nextLoopMD,
// loopMDDefs, vectorizeHint, parallelAccessHint, parallelAccessGroupRef)
// — those are native-owned-function emission state, separate from the
// MIR ostyEmitter path. They zero-init here, matching the original
// `&LlvmEmitter{temp: g.seq.TempSeq, body: nil}` semantics.
//
// Osty: MirSeq.ostyEmitter
func (s *MirSeq) OstyEmitter() *LlvmEmitter {
	return &LlvmEmitter{
		temp:              s.TempSeq,
		label:             0,
		stringId:          0,
		body:              nil,
		locals:            nil,
		stringGlobals:     nil,
		nativeBoundedLens: nil,
		nativeSafeIndices: nil,
		nativeListData:    nil,
		nativeListLens:    nil,
	}
}

// LLVM-line builders. Each helper produces one fully-formed function-
// body line ending with `\n`. Osty: toolchain/mir_generator.osty.

// mirStoreLine renders `  store <ty> <val>, ptr <slot>\n`.
// Osty: mirStoreLine
func mirStoreLine(ty string, val string, slot string) string {
	return "  store " + ty + " " + val + ", ptr " + slot + "\n"
}

// mirCallVoidLine renders `  call void @<sym>(<argList>)\n`.
// Osty: mirCallVoidLine
func mirCallVoidLine(sym string, argList string) string {
	return "  call void @" + sym + "(" + argList + ")\n"
}

// mirCallValueLine renders `  <reg> = call <retTy> @<sym>(<argList>)\n`.
// Osty: mirCallValueLine
func mirCallValueLine(reg string, retTy string, sym string, argList string) string {
	return "  " + reg + " = call " + retTy + " @" + sym + "(" + argList + ")\n"
}

// mirCallStmtLine renders `  call <retTy> @<sym>(<argList>)\n` — the
// statement-position call where the return value is discarded.
// Osty: mirCallStmtLine
func mirCallStmtLine(retTy string, sym string, argList string) string {
	return "  call " + retTy + " @" + sym + "(" + argList + ")\n"
}

// mirCallStmtNoArgsLine renders `  call <retTy> @<sym>()\n` — the
// no-argument variant of `mirCallStmtLine`. Used by bounded thread
// poll helpers whose result is conditionally retained.
// Osty: mirCallStmtNoArgsLine
func mirCallStmtNoArgsLine(retTy string, sym string) string {
	return "  call " + retTy + " @" + sym + "()\n"
}

// mirGEPInboundsI8Line renders the byte-stride GEP form:
// `  <reg> = getelementptr inbounds i8, ptr <basePtr>, i64 <offDigits>\n`.
// Osty: mirGEPInboundsI8Line
func mirGEPInboundsI8Line(reg string, basePtr string, offDigits string) string {
	return "  " + reg + " = getelementptr inbounds i8, ptr " + basePtr +
		", i64 " + offDigits + "\n"
}

// mirLoadLine renders `  <reg> = load <ty>, ptr <ptr>\n`.
// Osty: mirLoadLine
func mirLoadLine(reg string, ty string, ptr string) string {
	return "  " + reg + " = load " + ty + ", ptr " + ptr + "\n"
}

// mirICmpEqLine renders `  <reg> = icmp eq <ty> <lhs>, <rhs>\n`.
// Osty: mirICmpEqLine
func mirICmpEqLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = icmp eq " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirBrCondLine renders the conditional branch
// `  br i1 <cond>, label %<trueLabel>, label %<falseLabel>\n`.
// Osty: mirBrCondLine
func mirBrCondLine(cond string, trueLabel string, falseLabel string) string {
	return "  br i1 " + cond + ", label %" + trueLabel + ", label %" + falseLabel + "\n"
}

// mirBrUncondLine renders `  br label %<label>\n`.
// Osty: mirBrUncondLine
func mirBrUncondLine(label string) string {
	return "  br label %" + label + "\n"
}

// mirLabelLine renders `<name>:\n`.
// Osty: mirLabelLine
func mirLabelLine(name string) string {
	return name + ":\n"
}

// mirLabelHeadWithBranch renders `<name>:\n  br label %<target>\n` —
// the head-of-block + tail-branch shape used at streq match /
// nomatch sites.
// Osty: mirLabelHeadWithBranch
func mirLabelHeadWithBranch(name string, target string) string {
	return name + ":\n  br label %" + target + "\n"
}

// mirPhiI1FromTwoLine renders the two-incoming-edge phi
// `  <reg> = phi i1 [true, %<trueLabel>], [false, %<falseLabel>]\n`.
// Osty: mirPhiI1FromTwoLine
func mirPhiI1FromTwoLine(reg string, trueLabel string, falseLabel string) string {
	return "  " + reg + " = phi i1 [true, %" + trueLabel +
		"], [false, %" + falseLabel + "]\n"
}

// mirXorI1NegLine renders the i1 negation `  <reg> = xor i1 <src>, true\n`.
// Osty: mirXorI1NegLine
func mirXorI1NegLine(reg string, src string) string {
	return "  " + reg + " = xor i1 " + src + ", true\n"
}

// mirStoreZeroinitLine renders `  store <ty> zeroinitializer, ptr <slot>\n`.
// Osty: mirStoreZeroinitLine
func mirStoreZeroinitLine(ty string, slot string) string {
	return "  store " + ty + " zeroinitializer, ptr " + slot + "\n"
}

// mirInsertValueAggLine renders the insertvalue shape:
// `  <reg> = insertvalue <aggTy> <baseVal>, <fieldTy> <val>, <idxDigits>\n`.
// Osty: mirInsertValueAggLine
func mirInsertValueAggLine(reg string, aggTy string, baseVal string, fieldTy string, val string, idxDigits string) string {
	return "  " + reg + " = insertvalue " + aggTy + " " + baseVal +
		", " + fieldTy + " " + val +
		", " + idxDigits + "\n"
}

// mirSubI64Line renders i64 subtraction `  <reg> = sub i64 <lhs>, <rhs>\n`.
// Osty: mirSubI64Line
func mirSubI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = sub i64 " + lhs + ", " + rhs + "\n"
}

// mirAddI64Line renders i64 addition `  <reg> = add i64 <lhs>, <rhs>\n`.
// Osty: mirAddI64Line
func mirAddI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = add i64 " + lhs + ", " + rhs + "\n"
}

// mirAndI64Line renders i64 bitwise-and `  <reg> = and i64 <lhs>, <rhs>\n`.
// Used by the SSO tag-bit check in `emitInlineStringEqLiteral`.
// Osty: mirAndI64Line
func mirAndI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = and i64 " + lhs + ", " + rhs + "\n"
}

// mirFCmpLine renders the general floating-point compare shape
// `  <reg> = fcmp <pred> <ty> <lhs>, <rhs>\n`.
// Osty: mirFCmpLine
func mirFCmpLine(reg string, pred string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = fcmp " + pred + " " + ty + " " + lhs + ", " + rhs + "\n"
}

// Generalised line builders — fixes the over-specialisation of the
// first slice. Specialised builders above stay as compat callers.

// mirGEPInboundsLine renders the general single-index GEP shape.
// Osty: mirGEPInboundsLine
func mirGEPInboundsLine(reg string, baseTy string, basePtr string, idxTy string, idx string) string {
	return "  " + reg + " = getelementptr inbounds " + baseTy +
		", ptr " + basePtr + ", " + idxTy + " " + idx + "\n"
}

// mirGEPStructFieldLine renders the two-index struct-field GEP form.
// Osty: mirGEPStructFieldLine
func mirGEPStructFieldLine(reg string, structTy string, basePtr string, fieldDigits string) string {
	return "  " + reg + " = getelementptr inbounds " + structTy +
		", ptr " + basePtr +
		", i32 0, i32 " + fieldDigits + "\n"
}

// mirICmpLine renders the general icmp shape with arbitrary predicate.
// Osty: mirICmpLine
func mirICmpLine(reg string, pred string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = icmp " + pred + " " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirAllocaLine renders `  <reg> = alloca <ty>\n`.
// Osty: mirAllocaLine
func mirAllocaLine(reg string, ty string) string {
	return "  " + reg + " = alloca " + ty + "\n"
}

// mirRetLine renders `  ret <ty> <val>\n`.
// Osty: mirRetLine
func mirRetLine(ty string, val string) string {
	return "  ret " + ty + " " + val + "\n"
}

// mirRetVoidLine renders `  ret void\n`.
// Osty: mirRetVoidLine
func mirRetVoidLine() string {
	return "  ret void\n"
}

// mirSelectLine renders the i1 select form.
// Osty: mirSelectLine
func mirSelectLine(reg string, ty string, cond string, lhs string, rhs string) string {
	return "  " + reg + " = select i1 " + cond +
		", " + ty + " " + lhs +
		", " + ty + " " + rhs + "\n"
}

// mirSExtLine renders sign-extension.
// Osty: mirSExtLine
func mirSExtLine(reg string, fromTy string, val string, toTy string) string {
	return "  " + reg + " = sext " + fromTy + " " + val + " to " + toTy + "\n"
}

// mirZExtLine renders zero-extension.
// Osty: mirZExtLine
func mirZExtLine(reg string, fromTy string, val string, toTy string) string {
	return "  " + reg + " = zext " + fromTy + " " + val + " to " + toTy + "\n"
}

// mirTruncLine renders truncation.
// Osty: mirTruncLine
func mirTruncLine(reg string, fromTy string, val string, toTy string) string {
	return "  " + reg + " = trunc " + fromTy + " " + val + " to " + toTy + "\n"
}

// mirPtrToIntLine renders ptr→int conversion.
// Osty: mirPtrToIntLine
func mirPtrToIntLine(reg string, val string, toTy string) string {
	return "  " + reg + " = ptrtoint ptr " + val + " to " + toTy + "\n"
}

// mirIntToPtrLine renders int→ptr conversion.
// Osty: mirIntToPtrLine
func mirIntToPtrLine(reg string, fromTy string, val string) string {
	return "  " + reg + " = inttoptr " + fromTy + " " + val + " to ptr\n"
}

// mirCommentLine renders `  ; <text>\n`.
// Osty: mirCommentLine
func mirCommentLine(text string) string {
	return "  ; " + text + "\n"
}

// mirExtractValueLine renders `  <reg> = extractvalue <aggTy> <aggVal>, <idxDigits>\n`.
// Osty: mirExtractValueLine
func mirExtractValueLine(reg string, aggTy string, aggVal string, idxDigits string) string {
	return "  " + reg + " = extractvalue " + aggTy + " " + aggVal + ", " + idxDigits + "\n"
}

// mirBitcastLine renders `  <reg> = bitcast <fromTy> <val> to <toTy>\n`.
// Osty: mirBitcastLine
func mirBitcastLine(reg string, fromTy string, val string, toTy string) string {
	return "  " + reg + " = bitcast " + fromTy + " " + val + " to " + toTy + "\n"
}

// mirPhiTwoLine renders the two-incoming-edge phi.
// Osty: mirPhiTwoLine
func mirPhiTwoLine(reg string, ty string, val1 string, label1 string, val2 string, label2 string) string {
	return "  " + reg + " = phi " + ty +
		" [ " + val1 + ", %" + label1 +
		" ], [ " + val2 + ", %" + label2 + " ]\n"
}

// mirCallVoidNoArgsLine renders `  call void @<sym>()\n`.
// Osty: mirCallVoidNoArgsLine
func mirCallVoidNoArgsLine(sym string) string {
	return "  call void @" + sym + "()\n"
}

// mirUnreachableLine renders `  unreachable\n`.
// Osty: mirUnreachableLine
func mirUnreachableLine() string {
	return "  unreachable\n"
}

// Specialised line builders for §1 vector-list fast-path metadata,
// §5 GC bounds checks, and §7 list / map intrinsic chains.

// mirAndI1Line renders i1 logical-and `  <reg> = and i1 <lhs>, <rhs>\n`.
// Osty: mirAndI1Line
func mirAndI1Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = and i1 " + lhs + ", " + rhs + "\n"
}

// mirMulI64Line renders i64 multiplication.
// Osty: mirMulI64Line
func mirMulI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = mul i64 " + lhs + ", " + rhs + "\n"
}

// mirSDivI64Line renders i64 signed division.
// Osty: mirSDivI64Line
func mirSDivI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = sdiv i64 " + lhs + ", " + rhs + "\n"
}

// mirCallValueNoArgsLine renders argumentless typed call
// `  <reg> = call <retTy> @<sym>()\n`.
// Osty: mirCallValueNoArgsLine
func mirCallValueNoArgsLine(reg string, retTy string, sym string) string {
	return "  " + reg + " = call " + retTy + " @" + sym + "()\n"
}

// mirCallValueWithAliasScopeLine renders a typed call with an
// `!alias.scope` metadata attachment.
// Osty: mirCallValueWithAliasScopeLine
func mirCallValueWithAliasScopeLine(reg string, retTy string, sym string, argList string, scopeRef string) string {
	return "  " + reg + " = call " + retTy + " @" + sym + "(" + argList +
		"), !alias.scope " + scopeRef + "\n"
}

// mirLoadWithNoAliasLine renders a load tagged with `!noalias` metadata.
// Osty: mirLoadWithNoAliasLine
func mirLoadWithNoAliasLine(reg string, ty string, ptr string, scopeRef string) string {
	return "  " + reg + " = load " + ty + ", ptr " + ptr + ", !noalias " + scopeRef + "\n"
}

// mirStoreWithNoAliasLine renders a store tagged with `!noalias` metadata.
// Osty: mirStoreWithNoAliasLine
func mirStoreWithNoAliasLine(ty string, val string, ptr string, scopeRef string) string {
	return "  store " + ty + " " + val + ", ptr " + ptr + ", !noalias " + scopeRef + "\n"
}

// mirCallVoidNoReturnNoArgsLine renders `  call void @<sym>() noreturn\n`.
// Osty: mirCallVoidNoReturnNoArgsLine
func mirCallVoidNoReturnNoArgsLine(sym string) string {
	return "  call void @" + sym + "() noreturn\n"
}

// mirAllocaArrayLine renders `  <reg> = alloca <ty>, i64 <countDigits>\n`.
// Osty: mirAllocaArrayLine
func mirAllocaArrayLine(reg string, ty string, countDigits string) string {
	return "  " + reg + " = alloca " + ty + ", i64 " + countDigits + "\n"
}

// mirGEPLine renders the non-inbounds GEP form.
// Osty: mirGEPLine
func mirGEPLine(reg string, baseTy string, basePtr string, idxTy string, idx string) string {
	return "  " + reg + " = getelementptr " + baseTy +
		", ptr " + basePtr + ", " + idxTy + " " + idx + "\n"
}

// mirStorePtrLine renders `  store ptr <val>, ptr <slot>\n`.
// Osty: mirStorePtrLine
func mirStorePtrLine(val string, slot string) string {
	return "  store ptr " + val + ", ptr " + slot + "\n"
}

// mirStoreNullPtrLine renders `  store ptr null, ptr <slot>\n` — the
// GC-managed slot zeroing pattern used by the entry safepoint preamble
// and the cancellation-aware Handle slot zeroer.
// Osty: mirStoreNullPtrLine
func mirStoreNullPtrLine(slot string) string {
	return "  store ptr null, ptr " + slot + "\n"
}

// mirRawAssignLine renders `  <reg> = <rhs>\n` — the catch-all for
// SSA assignments whose right-hand side is computed by a separate
// formatter (e.g. `mirUnaryInstruction`, predicate string concat).
// Osty: mirRawAssignLine
func mirRawAssignLine(reg string, rhs string) string {
	return "  " + reg + " = " + rhs + "\n"
}

// mirBinaryOpLine renders `  <reg> = <opcode> <ty> <lhs>, <rhs>\n` —
// the canonical two-operand instruction shape that `emitBinary`
// lowers to after picking the opcode via `mirBinaryOpcode`.
// Osty: mirBinaryOpLine
func mirBinaryOpLine(reg string, opcode string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = " + opcode + " " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirICmpLineFromPred is a thin alias of `mirICmpLine` used at sites
// that thread the predicate string from `mirBinaryOpcode`.
// Osty: mirICmpLineFromPred
func mirICmpLineFromPred(reg string, pred string, ty string, lhs string, rhs string) string {
	return mirICmpLine(reg, pred, ty, lhs, rhs)
}

// mirAllocaWithStoreLine renders `  <reg> = alloca <ty>` followed by
// `  store <ty> <init>, ptr <reg>` — the canonical "freshly-zeroed
// scalar slot" preamble. Used by the loop-safepoint poll counter and
// the cancellation flag slot.
// Osty: mirAllocaWithStoreLine
func mirAllocaWithStoreLine(reg string, ty string, init string) string {
	return mirAllocaLine(reg, ty) + mirStoreLine(ty, init, reg)
}

// mirAllocaWithStorePtrLine is the pointer-typed variant —
// `alloca ptr` then `store ptr <val>, ptr <reg>`. Closure-thunk
// materialization and indirect-call argument prep both spell this out
// inline; the named builder captures the intent.
// Osty: mirAllocaWithStorePtrLine
func mirAllocaWithStorePtrLine(reg string, val string) string {
	return mirAllocaLine(reg, "ptr") + mirStorePtrLine(val, reg)
}

// mirAllocaWithStoreNullPtrLine renders `alloca ptr` + `store ptr
// null` at once — the canonical zero-init managed-pointer slot used
// by `emitNullaryRV` for `Some(None)` payload synthesis and the GC
// root preamble for non-param roots.
// Osty: mirAllocaWithStoreNullPtrLine
func mirAllocaWithStoreNullPtrLine(reg string) string {
	return mirAllocaLine(reg, "ptr") + mirStoreNullPtrLine(reg)
}

// §14 enum / tuple layout cache.
//
// MirLayoutCache mirrors `toolchain/mir_generator.osty: MirLayoutCache`.
// The struct owns the dedup + insertion-order side of the emitter's
// aggregate-type pool. The matching map values (`g.tupleDefs
// map[string][]mir.Type`) stay on the Go side because their payload
// type is `mir.Type` — a Go interface that has no Osty mirror.

// Osty: MirLayoutCache
type MirLayoutCache struct {
	EnumLayoutOrder []string
	TupleOrder      []string
}

// Osty: MirLayoutCache.registerEnumLayout
func (c *MirLayoutCache) RegisterEnumLayout(name string) bool {
	for _, existing := range c.EnumLayoutOrder {
		if existing == name {
			return false
		}
	}
	c.EnumLayoutOrder = append(c.EnumLayoutOrder, name)
	return true
}

// Osty: MirLayoutCache.registerTuple
func (c *MirLayoutCache) RegisterTuple(name string) bool {
	for _, existing := range c.TupleOrder {
		if existing == name {
			return false
		}
	}
	c.TupleOrder = append(c.TupleOrder, name)
	return true
}

// Osty: MirLayoutCache.isEmpty
func (c *MirLayoutCache) IsEmpty() bool {
	return len(c.EnumLayoutOrder) == 0 && len(c.TupleOrder) == 0
}

// §9 terminator templates. Unit-return / typed-return / unreachable
// shapes route through the existing line builders (`mirRetVoidLine`,
// `mirLoadLine` + `mirRetLine`, `mirUnreachableLine`); this section
// only owns the terminator-specific shapes that don't fit the
// per-instruction line-builder mold.

// Osty: mirTerminatorBranchUnconditional
func mirTerminatorBranchUnconditional(targetLabel, loopMDRef string) string {
	if loopMDRef != "" {
		return "  br label %" + targetLabel + ", !llvm.loop " + loopMDRef + "\n"
	}
	return "  br label %" + targetLabel + "\n"
}

// Osty: mirTerminatorBranchConditional
func mirTerminatorBranchConditional(condReg, thenLabel, elseLabel, loopMDRef string) string {
	head := "  br i1 " + condReg + ", label %" + thenLabel + ", label %" + elseLabel
	if loopMDRef != "" {
		return head + ", !llvm.loop " + loopMDRef + "\n"
	}
	return head + "\n"
}

// Osty: MirGenSwitchCase
type MirGenSwitchCase struct {
	ValueText   string
	TargetLabel string
}

// Osty: mirTerminatorSwitchInt
//
// Builds the switch IR text via `strings.Builder` rather than the
// `out += ...` shape the Osty source uses literally — large enum
// dispatches with hundreds of cases would otherwise be O(n²) on the
// Go side. The emitted bytes are identical.
func mirTerminatorSwitchInt(llvmType, scrutReg, defaultLabel string, cases []MirGenSwitchCase) string {
	var b llvmStrings.Builder
	b.WriteString("  switch ")
	b.WriteString(llvmType)
	b.WriteByte(' ')
	b.WriteString(scrutReg)
	b.WriteString(", label %")
	b.WriteString(defaultLabel)
	b.WriteString(" [\n")
	for _, c := range cases {
		b.WriteString("    ")
		b.WriteString(llvmType)
		b.WriteByte(' ')
		b.WriteString(c.ValueText)
		b.WriteString(", label %")
		b.WriteString(c.TargetLabel)
		b.WriteByte('\n')
	}
	b.WriteString("  ]\n")
	return b.String()
}

// Osty: mirTerminatorReturnMain
func mirTerminatorReturnMain() string {
	return "  ret i32 0\n"
}

// §3 runtime declaration cache.
//
// MirRuntimeDecls mirrors `toolchain/mir_generator.osty: MirRuntimeDecls`.
// The struct owns the dedup + insertion-order side of the emitter's
// runtime forward-declaration pool — the `declare <ret> @<sym>(<args>)`
// lines that prepend any module calling `osty_rt_*` runtime symbols.
// Replaces the `mirGen.declares map[string]string + declareOrder []string`
// pair fields. Sibling of `MirLayoutCache` (#888) which does the same
// dedup-with-order job for aggregate type defs.

// Osty: MirRuntimeDecls
type MirRuntimeDecls struct {
	Names      []string
	Signatures map[string]string
}

// Osty: MirRuntimeDecls.declare
func (c *MirRuntimeDecls) Declare(name string, signature string) bool {
	if c.Signatures == nil {
		c.Signatures = map[string]string{}
	}
	if _, ok := c.Signatures[name]; ok {
		return false
	}
	c.Signatures[name] = signature
	c.Names = append(c.Names, name)
	return true
}

// Osty: MirRuntimeDecls.signature
func (c *MirRuntimeDecls) Signature(name string) string {
	if c.Signatures == nil {
		return ""
	}
	return c.Signatures[name]
}

// Osty: MirRuntimeDecls.orderedSignatures
func (c *MirRuntimeDecls) OrderedSignatures() []string {
	out := make([]string, 0, len(c.Names))
	for _, name := range c.Names {
		out = append(out, c.Signature(name))
	}
	return out
}

// Osty: MirRuntimeDecls.isEmpty
func (c *MirRuntimeDecls) IsEmpty() bool {
	return len(c.Names) == 0
}

// §12 string-literal interning pool.
//
// MirStringPool mirrors `toolchain/mir_generator.osty: MirStringPool`.
// Owns the dedup + insertion-order side of the emitter's string-literal
// pool. Replaces the `mirGen.strings map[string]string + stringOrder
// []string` pair fields. Sibling of `MirRuntimeDecls` and
// `MirLayoutCache` — the third member of the dedup-with-order family.

// Osty: MirStringPool
type MirStringPool struct {
	ByContent map[string]string
	Order     []string
}

// Osty: MirStringPool.intern
func (p *MirStringPool) Intern(content string) string {
	if p.ByContent == nil {
		p.ByContent = map[string]string{}
	}
	if sym, ok := p.ByContent[content]; ok {
		return sym
	}
	sym := "@.str." + strconv.Itoa(len(p.Order))
	p.ByContent[content] = sym
	p.Order = append(p.Order, content)
	return sym
}

// Osty: MirStringPool.symbol
func (p *MirStringPool) Symbol(content string) string {
	if p.ByContent == nil {
		return ""
	}
	return p.ByContent[content]
}

// Osty: MirStringPool.orderedKeys
func (p *MirStringPool) OrderedKeys() []string {
	return p.Order
}

// Osty: MirStringPool.isEmpty
func (p *MirStringPool) IsEmpty() bool {
	return len(p.Order) == 0
}

// §4 closure-thunk definition cache.
//
// MirThunkDefs mirrors `toolchain/mir_generator.osty: MirThunkDefs`.
// Owns the dedup + insertion-order side of the emitter's closure-thunk
// pool. Replaces the `mirGen.thunkDefs map[string]string + thunkOrder
// []string` pair fields. Fourth member of the dedup-with-order family
// alongside `MirLayoutCache`, `MirRuntimeDecls`, and `MirStringPool`.

// Osty: MirThunkDefs
type MirThunkDefs struct {
	Bodies map[string]string
	Order  []string
}

// Osty: MirThunkDefs.contains
func (t *MirThunkDefs) Contains(symbol string) bool {
	if t.Bodies == nil {
		return false
	}
	_, ok := t.Bodies[symbol]
	return ok
}

// Osty: MirThunkDefs.register
func (t *MirThunkDefs) Register(symbol string, body string) bool {
	if t.Bodies == nil {
		t.Bodies = map[string]string{}
	}
	if _, ok := t.Bodies[symbol]; ok {
		return false
	}
	t.Bodies[symbol] = body
	t.Order = append(t.Order, symbol)
	return true
}

// Osty: MirThunkDefs.body
func (t *MirThunkDefs) Body(symbol string) string {
	if t.Bodies == nil {
		return ""
	}
	return t.Bodies[symbol]
}

// Osty: MirThunkDefs.orderedBodies
func (t *MirThunkDefs) OrderedBodies() []string {
	out := make([]string, 0, len(t.Order))
	for _, sym := range t.Order {
		out = append(out, t.Body(sym))
	}
	return out
}

// Osty: MirThunkDefs.isEmpty
func (t *MirThunkDefs) IsEmpty() bool {
	return len(t.Order) == 0
}

// §3 vtable reference set (downcast support).
//
// MirVtableRefs mirrors `toolchain/mir_generator.osty: MirVtableRefs`.
// Owns the dedup + insertion-order side of the emitter's vtable-symbol
// pool. Replaces the `mirGen.vtableRefs map[string]struct{} +
// vtableRefOrder []string` pair fields. Fifth member of the
// dedup-with-order family alongside `MirLayoutCache`, `MirRuntimeDecls`,
// `MirStringPool`, and `MirThunkDefs`.

// Osty: MirVtableRefs
type MirVtableRefs struct {
	Seen  map[string]bool
	Order []string
}

// Osty: MirVtableRefs.register
func (v *MirVtableRefs) Register(symbol string) bool {
	if v.Seen == nil {
		v.Seen = map[string]bool{}
	}
	if _, ok := v.Seen[symbol]; ok {
		return false
	}
	v.Seen[symbol] = true
	v.Order = append(v.Order, symbol)
	return true
}

// Osty: MirVtableRefs.contains
func (v *MirVtableRefs) Contains(symbol string) bool {
	if v.Seen == nil {
		return false
	}
	_, ok := v.Seen[symbol]
	return ok
}

// Osty: MirVtableRefs.orderedSymbols
func (v *MirVtableRefs) OrderedSymbols() []string {
	return v.Order
}

// Osty: MirVtableRefs.isEmpty
func (v *MirVtableRefs) IsEmpty() bool {
	return len(v.Order) == 0
}

// §4 function emission templates.
//
// Pure shape builders for the function header / param list /
// external-declare / cconv keyword shapes. The state-bearing
// orchestration in `emitFunction` (block-label allocation,
// per-fn flag capture, alloca preamble, fnBuf flush) stays on
// the Go side — these helpers cover the LLVM-text leaves that
// don't depend on `mirGen` state.

// mirCConvKeyword returns the LLVM calling-convention keyword
// (`"ccc "` for `#[c_abi]` / `""` for default). Trailing space
// is part of the return — caller splices directly between
// `define `/`declare ` and the return type.
// Osty: mirCConvKeyword
func mirCConvKeyword(cabi bool) string {
	if cabi {
		return "ccc "
	}
	return ""
}

// mirParamIsNoalias decides whether a single parameter should
// receive the LLVM `noalias` attribute. Mirrors the predicate
// from `paramIsNoalias` but takes the parameter's source-name
// + the noalias-set as plain strings so the Osty side can
// implement the check without modeling `*mir.Local`.
// Osty: mirParamIsNoalias
func mirParamIsNoalias(llvmT string, locName string, noaliasAll bool, noaliasNames []string) bool {
	if llvmT != "ptr" {
		return false
	}
	if noaliasAll {
		return true
	}
	for _, n := range noaliasNames {
		if n == locName {
			return true
		}
	}
	return false
}

// mirFunctionParamPart renders one parameter entry of a function
// signature: `<llvmT>[ noalias] %arg<idxDigits>`.
// Osty: mirFunctionParamPart
func mirFunctionParamPart(llvmT string, isNoalias bool, idxDigits string) string {
	if isNoalias {
		return llvmT + " noalias %arg" + idxDigits
	}
	return llvmT + " %arg" + idxDigits
}

// mirBlockLabelName returns `"entry"` when isEntry / `"bb<N>"`
// otherwise. `blockIDDigits` is the already-formatted decimal
// block ID.
// Osty: mirBlockLabelName
func mirBlockLabelName(isEntry bool, blockIDDigits string) string {
	if isEntry {
		return "entry"
	}
	return "bb" + blockIDDigits
}

// mirExternalDeclareLine renders the `declare` line for an
// external function. Trailing `\n\n` matches legacy spacing.
// Osty: mirExternalDeclareLine
func mirExternalDeclareLine(cconv string, retLLVM string, name string, paramListJoined string, attrs string) string {
	attrSuffix := ""
	if attrs != "" {
		attrSuffix = " " + attrs
	}
	return "declare " + cconv + retLLVM + " @" + name + "(" + paramListJoined + ")" + attrSuffix + "\n\n"
}

// mirFunctionDefineHeader renders the opening line of a function
// definition (the `{` is included; the body / closing `}` come
// from the caller).
// Osty: mirFunctionDefineHeader
func mirFunctionDefineHeader(cconv string, retLLVM string, name string, paramListJoined string, attrs string) string {
	attrSuffix := ""
	if attrs != "" {
		attrSuffix = " " + attrs
	}
	return "define " + cconv + retLLVM + " @" + name + "(" + paramListJoined + ")" + attrSuffix + " {\n"
}

// mirFunctionDefineFooter renders the closing `}\n\n` of a function
// definition.
// Osty: mirFunctionDefineFooter
func mirFunctionDefineFooter() string {
	return "}\n\n"
}

// mirInlineAsmIdentityCallLine renders the LLVM inline-asm
// identity-barrier shape used by `std.hint.black_box(x)`.
// Osty: mirInlineAsmIdentityCallLine
func mirInlineAsmIdentityCallLine(reg string, ty string, val string) string {
	return "  " + reg + " = call " + ty +
		` asm sideeffect "", "=r,0"(` + ty + " " + val + ")\n"
}

// mirCallVarargPrintfLine renders the LLVM-IR printf call shape
// `  call i32 (ptr, ...) @printf(ptr <fmt>[, <args>])\n`.
// Osty: mirCallVarargPrintfLine
func mirCallVarargPrintfLine(fmtSym string, restArgs string) string {
	if restArgs == "" {
		return "  call i32 (ptr, ...) @printf(ptr " + fmtSym + ")\n"
	}
	return "  call i32 (ptr, ...) @printf(ptr " + fmtSym + ", " + restArgs + ")\n"
}

// mirCallExitLine renders the noreturn `exit(N)` runtime hook
// `  call void @exit(i32 <codeDigits>)\n`.
// Osty: mirCallExitLine
func mirCallExitLine(codeDigits string) string {
	return "  call void @exit(i32 " + codeDigits + ")\n"
}

// mirBrCondReversedLine renders the conditional branch with true /
// false target order swapped — used by `emitTestingFailureCheck`
// for the `assertFalse` shape where failure is on cond=true.
// Osty: mirBrCondReversedLine
func mirBrCondReversedLine(cond string, falseLabel string, trueLabel string) string {
	return "  br i1 " + cond + ", label %" + falseLabel + ", label %" + trueLabel + "\n"
}

// mirCallIndirectVoidLine renders the void-return indirect call
// `  call <callType> <fnPtrReg>(<argList>)\n`.
// Osty: mirCallIndirectVoidLine
func mirCallIndirectVoidLine(callType string, fnPtrReg string, argList string) string {
	return "  call " + callType + " " + fnPtrReg + "(" + argList + ")\n"
}

// mirCallIndirectValueLine renders the typed indirect call
// `  <reg> = call <callType> <fnPtrReg>(<argList>)\n`.
// Osty: mirCallIndirectValueLine
func mirCallIndirectValueLine(reg string, callType string, fnPtrReg string, argList string) string {
	return "  " + reg + " = call " + callType + " " + fnPtrReg + "(" + argList + ")\n"
}

// Higher-level Option / Result aggregate builders.

// MirAggregatePair captures the output of an Option / Result
// 2-step aggregate construction.
// Osty: MirAggregatePair
type MirAggregatePair struct {
	Step1Reg string
	FinalReg string
	Lines    []string
}

// mirSomeI64Aggregate builds the Some(payload) shape (2 insertvalue lines).
// Osty: mirSomeI64Aggregate
func mirSomeI64Aggregate(step1Reg string, finalReg string, optLLVM string, payloadI64 string) MirAggregatePair {
	return MirAggregatePair{
		Step1Reg: step1Reg,
		FinalReg: finalReg,
		Lines: []string{
			mirInsertValueAggLine(step1Reg, optLLVM, "undef", "i64", "1", "0"),
			mirInsertValueAggLine(finalReg, optLLVM, step1Reg, "i64", payloadI64, "1"),
		},
	}
}

// mirNoneAggregate builds the None shape (disc=0, payload=0).
// Osty: mirNoneAggregate
func mirNoneAggregate(step1Reg string, finalReg string, optLLVM string) MirAggregatePair {
	return MirAggregatePair{
		Step1Reg: step1Reg,
		FinalReg: finalReg,
		Lines: []string{
			mirInsertValueAggLine(step1Reg, optLLVM, "undef", "i64", "0", "0"),
			mirInsertValueAggLine(finalReg, optLLVM, step1Reg, "i64", "0", "1"),
		},
	}
}

// mirResultOkI64Aggregate builds the Ok(payload) shape.
// Osty: mirResultOkI64Aggregate
func mirResultOkI64Aggregate(step1Reg string, finalReg string, resultLLVM string, payloadI64 string) MirAggregatePair {
	return MirAggregatePair{
		Step1Reg: step1Reg,
		FinalReg: finalReg,
		Lines: []string{
			mirInsertValueAggLine(step1Reg, resultLLVM, "undef", "i64", "1", "0"),
			mirInsertValueAggLine(finalReg, resultLLVM, step1Reg, "i64", payloadI64, "1"),
		},
	}
}

// mirResultErrI64Aggregate builds the Err(payload) shape.
// Osty: mirResultErrI64Aggregate
func mirResultErrI64Aggregate(step1Reg string, finalReg string, resultLLVM string, payloadI64 string) MirAggregatePair {
	return MirAggregatePair{
		Step1Reg: step1Reg,
		FinalReg: finalReg,
		Lines: []string{
			mirInsertValueAggLine(step1Reg, resultLLVM, "undef", "i64", "0", "0"),
			mirInsertValueAggLine(finalReg, resultLLVM, step1Reg, "i64", payloadI64, "1"),
		},
	}
}

// mirGCAllocCallLine renders the GC heap allocator call.
// Osty: mirGCAllocCallLine
func mirGCAllocCallLine(reg string, traceKindDigits string, size string, site string) string {
	return "  " + reg + " = call ptr @osty.gc.alloc_v1(i64 " +
		traceKindDigits + ", i64 " + size + ", ptr " + site + ")\n"
}

// mirFPTruncDoubleToFloatLine renders FP-truncate from double to float.
// Osty: mirFPTruncDoubleToFloatLine
func mirFPTruncDoubleToFloatLine(reg string, val string) string {
	return "  " + reg + " = fptrunc double " + val + " to float\n"
}

// mirFPExtFloatToDoubleLine renders FP-extend from float to double.
// Osty: mirFPExtFloatToDoubleLine
func mirFPExtFloatToDoubleLine(reg string, val string) string {
	return "  " + reg + " = fpext float " + val + " to double\n"
}

// §10 cast / arithmetic builders.

// mirSIToFPLine renders signed-int → float cast.
// Osty: mirSIToFPLine
func mirSIToFPLine(reg string, fromTy string, val string, toTy string) string {
	return "  " + reg + " = sitofp " + fromTy + " " + val + " to " + toTy + "\n"
}

// mirFPToSILine renders float → signed-int cast.
// Osty: mirFPToSILine
func mirFPToSILine(reg string, fromTy string, val string, toTy string) string {
	return "  " + reg + " = fptosi " + fromTy + " " + val + " to " + toTy + "\n"
}

// mirFPResizeLine renders fpext / fptrunc with a parameterised opcode.
// Osty: mirFPResizeLine
func mirFPResizeLine(reg string, op string, fromTy string, val string, toTy string) string {
	return "  " + reg + " = " + op + " " + fromTy + " " + val + " to " + toTy + "\n"
}

// mirIntResizeLine renders sext / trunc with a parameterised opcode.
// Osty: mirIntResizeLine
func mirIntResizeLine(reg string, op string, fromTy string, val string, toTy string) string {
	return "  " + reg + " = " + op + " " + fromTy + " " + val + " to " + toTy + "\n"
}

// mirOrI1Line renders i1 logical-or.
// Osty: mirOrI1Line
func mirOrI1Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = or i1 " + lhs + ", " + rhs + "\n"
}

// mirShlI64Line renders i64 left shift.
// Osty: mirShlI64Line
func mirShlI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = shl i64 " + lhs + ", " + rhs + "\n"
}

// mirAShrI64Line renders i64 arithmetic shift right.
// Osty: mirAShrI64Line
func mirAShrI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = ashr i64 " + lhs + ", " + rhs + "\n"
}

// mirLShrI64Line renders i64 logical shift right.
// Osty: mirLShrI64Line
func mirLShrI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = lshr i64 " + lhs + ", " + rhs + "\n"
}

// mirFAddLine renders floating-point addition.
// Osty: mirFAddLine
func mirFAddLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = fadd " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirFSubLine renders floating-point subtraction.
// Osty: mirFSubLine
func mirFSubLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = fsub " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirFMulLine renders floating-point multiplication.
// Osty: mirFMulLine
func mirFMulLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = fmul " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirFDivLine renders floating-point division.
// Osty: mirFDivLine
func mirFDivLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = fdiv " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirFNegLine renders floating-point negation.
// Osty: mirFNegLine
func mirFNegLine(reg string, ty string, val string) string {
	return "  " + reg + " = fneg " + ty + " " + val + "\n"
}

// mirSubGenericLine renders integer subtraction at an arbitrary width.
// Osty: mirSubGenericLine
func mirSubGenericLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = sub " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirAddGenericLine renders integer addition at an arbitrary width.
// Osty: mirAddGenericLine
func mirAddGenericLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = add " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirMulGenericLine renders integer multiplication at an arbitrary width.
// Osty: mirMulGenericLine
func mirMulGenericLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = mul " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirSDivGenericLine renders integer signed division.
// Osty: mirSDivGenericLine
func mirSDivGenericLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = sdiv " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirSRemGenericLine renders integer signed modulo.
// Osty: mirSRemGenericLine
func mirSRemGenericLine(reg string, ty string, lhs string, rhs string) string {
	return "  " + reg + " = srem " + ty + " " + lhs + ", " + rhs + "\n"
}

// mirURemI64Line renders i64 unsigned modulo.
// Osty: mirURemI64Line
func mirURemI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = urem i64 " + lhs + ", " + rhs + "\n"
}

// mirXorI64Line renders i64 bitwise xor.
// Osty: mirXorI64Line
func mirXorI64Line(reg string, lhs string, rhs string) string {
	return "  " + reg + " = xor i64 " + lhs + ", " + rhs + "\n"
}

// §6 emit-shape builders — Go mirror of the helpers appended to
// `toolchain/mir_generator.osty`. Each replaces a 4–13 line inline
// `g.fnBuf.WriteString(...)` chain with a single named helper.

// mirCallI64MapKeyDeltaLine renders the fused map.incr ABI call:
// `  <reg> = call i64 @<sym>(ptr <map>, <keyLLVM> <key>, i64 <delta>)\n`.
// Osty: mirCallI64MapKeyDeltaLine
func mirCallI64MapKeyDeltaLine(reg, sym, mapReg, keyLLVM, key, delta string) string {
	return mirCallValueLine(reg, "i64", sym, "ptr "+mapReg+", "+keyLLVM+" "+key+", i64 "+delta)
}

// mirCallVoidMapKeyValuePtrLine renders the runtime map-set ABI:
// `  call void @<sym>(ptr <map>, <keyLLVM> <key>, ptr <valSlot>)\n`.
// Osty: mirCallVoidMapKeyValuePtrLine
func mirCallVoidMapKeyValuePtrLine(sym, mapReg, keyLLVM, key, valSlot string) string {
	return mirCallVoidLine(sym, "ptr "+mapReg+", "+keyLLVM+" "+key+", ptr "+valSlot)
}

// mirCallI1MapKeyOutPtrLine renders the runtime map-probe ABI:
// `  <reg> = call i1 @<sym>(ptr <map>, <keyLLVM> <key>, ptr <outSlot>)\n`.
// Osty: mirCallI1MapKeyOutPtrLine
func mirCallI1MapKeyOutPtrLine(reg, sym, mapReg, keyLLVM, key, outSlot string) string {
	return mirCallValueLine(reg, "i1", sym, "ptr "+mapReg+", "+keyLLVM+" "+key+", ptr "+outSlot)
}

// mirCallVoidListPtrI64ValueLine renders the typed-element list-set
// runtime ABI: `  call void @<sym>(ptr <list>, i64 <idx>, <elemLLVM>
// <val>)\n`.
// Osty: mirCallVoidListPtrI64ValueLine
func mirCallVoidListPtrI64ValueLine(sym, listReg, idxReg, elemLLVM, valReg string) string {
	return mirCallVoidLine(sym, "ptr "+listReg+", i64 "+idxReg+", "+elemLLVM+" "+valReg)
}

// mirCallVoidListBytesV1SetLine renders the bytes-v1 list-set ABI:
// `  call void @<sym>(ptr <list>, i64 <idx>, ptr <slot>, i64 <size>)\n`.
// Osty: mirCallVoidListBytesV1SetLine
func mirCallVoidListBytesV1SetLine(sym, listReg, idxReg, slot, size string) string {
	return mirCallVoidLine(sym, "ptr "+listReg+", i64 "+idxReg+", ptr "+slot+", i64 "+size)
}

// mirCallVoidListBytesV1GetLine renders the bytes-v1 list-get ABI:
// `  call void @<sym>(ptr <list>, i64 <idx>, ptr <out>, i64 <size>)\n`.
// Osty: mirCallVoidListBytesV1GetLine
func mirCallVoidListBytesV1GetLine(sym, listReg, idxReg, outSlot, size string) string {
	return mirCallVoidLine(sym, "ptr "+listReg+", i64 "+idxReg+", ptr "+outSlot+", i64 "+size)
}

// mirCallVoidListPushBytesV1Line renders the bytes-v1 list-push ABI:
// `  call void @<sym>(ptr <list>, ptr <slot>, i64 <size>)\n`.
// Osty: mirCallVoidListPushBytesV1Line
func mirCallVoidListPushBytesV1Line(sym, listReg, slot, size string) string {
	return mirCallVoidLine(sym, "ptr "+listReg+", ptr "+slot+", i64 "+size)
}

// mirGEPNullSizeLine renders `getelementptr <ty>, ptr null, i32 1`
// — the size-of stride GEP.
// Osty: mirGEPNullSizeLine
func mirGEPNullSizeLine(reg, ty string) string {
	return "  " + reg + " = getelementptr " + ty + ", ptr null, i32 1\n"
}

// mirPtrToIntSizeLine renders `<reg> = ptrtoint ptr <gepReg> to i64`
// — second half of the GEP-null sizeof idiom.
// Osty: mirPtrToIntSizeLine
func mirPtrToIntSizeLine(reg, gepReg string) string {
	return "  " + reg + " = ptrtoint ptr " + gepReg + " to i64\n"
}

// mirSizeOfLines renders both halves of the GEP-null sizeof idiom
// in one call. Returns a two-line block.
// Osty: mirSizeOfLines
func mirSizeOfLines(gepReg, sizeReg, ty string) string {
	return mirGEPNullSizeLine(gepReg, ty) + mirPtrToIntSizeLine(sizeReg, gepReg)
}

// mirThunkHeaderLine renders the opening line of a closure-thunk
// definition: `define private <retLLVM> @<thunkName>(<headerParams>) {`.
// Osty: mirThunkHeaderLine
func mirThunkHeaderLine(retLLVM, thunkName, headerParams string) string {
	return "define private " + retLLVM + " @" + thunkName + "(" + headerParams + ") {\n"
}

// mirThunkEntryLine returns the literal `entry:\n` block label.
// Osty: mirThunkEntryLine
func mirThunkEntryLine() string {
	return "entry:\n"
}

// mirThunkVoidCallLine renders the body of a void-return thunk.
// Osty: mirThunkVoidCallLine
func mirThunkVoidCallLine(symbol, argList string) string {
	return "  call void @" + symbol + "(" + argList + ")\n  ret void\n"
}

// mirThunkValueCallLine renders the body of a value-return thunk.
// Osty: mirThunkValueCallLine
func mirThunkValueCallLine(retLLVM, symbol, argList string) string {
	return "  %ret = call " + retLLVM + " @" + symbol + "(" + argList + ")\n  ret " + retLLVM + " %ret\n"
}

// mirThunkFooterLine returns the literal `}\n\n` that closes a
// thunk definition.
// Osty: mirThunkFooterLine
func mirThunkFooterLine() string {
	return "}\n\n"
}

// mirThunkBody assembles the entire closure-thunk text in one call.
// Osty: mirThunkBody
func mirThunkBody(retLLVM, thunkName, symbol, headerParams, argList string) string {
	header := mirThunkHeaderLine(retLLVM, thunkName, headerParams)
	entry := mirThunkEntryLine()
	var body string
	if retLLVM == "void" {
		body = mirThunkVoidCallLine(symbol, argList)
	} else {
		body = mirThunkValueCallLine(retLLVM, symbol, argList)
	}
	footer := mirThunkFooterLine()
	return header + entry + body + footer
}

// mirThunkParamPart renders one parameter entry of a thunk's
// user-param list: `<llvmT> %arg<idxDigits>`.
// Osty: mirThunkParamPart
func mirThunkParamPart(llvmT, idxDigits string) string {
	return llvmT + " %arg" + idxDigits
}

// mirCallVarargPrintfPathLine renders the path-prefixed printf
// shape: `  call i32 (ptr, ...) @printf(ptr <fmt>, ptr <path>)\n`.
// Osty: mirCallVarargPrintfPathLine
func mirCallVarargPrintfPathLine(fmtSym, pathSym string) string {
	return mirCallVarargPrintfLine(fmtSym, "ptr "+pathSym)
}

// mirICmpEqI64Line renders the eq-on-i64 specialisation.
// Osty: mirICmpEqI64Line
func mirICmpEqI64Line(reg, lhs, rhs string) string {
	return mirICmpEqLine(reg, "i64", lhs, rhs)
}

// mirICmpEqI1Line renders the eq-on-i1 specialisation.
// Osty: mirICmpEqI1Line
func mirICmpEqI1Line(reg, lhs, rhs string) string {
	return mirICmpEqLine(reg, "i1", lhs, rhs)
}

// mirICmpEqPtrLine renders the eq-on-ptr specialisation.
// Osty: mirICmpEqPtrLine
func mirICmpEqPtrLine(reg, lhs, rhs string) string {
	return mirICmpEqLine(reg, "ptr", lhs, rhs)
}

// mirCallVarargPrintfTwoArgLine renders the printf shape with two
// variadic args.
// Osty: mirCallVarargPrintfTwoArgLine
func mirCallVarargPrintfTwoArgLine(fmtSym, arg1, arg2 string) string {
	return mirCallVarargPrintfLine(fmtSym, arg1+", "+arg2)
}

// mirCallVarargPrintfThreeArgLine renders printf with three
// variadic args.
// Osty: mirCallVarargPrintfThreeArgLine
func mirCallVarargPrintfThreeArgLine(fmtSym, arg1, arg2, arg3 string) string {
	return mirCallVarargPrintfLine(fmtSym, arg1+", "+arg2+", "+arg3)
}

// mirAllocaSpillStoreLine renders alloca + store for spilling an
// SSA value into a stack slot before a bytes-v1 runtime call.
// Osty: mirAllocaSpillStoreLine
func mirAllocaSpillStoreLine(slot, ty, val string) string {
	return mirAllocaLine(slot, ty) + mirStoreLine(ty, val, slot)
}

// mirCallVoidListBytesV1SetWithBarrierLine renders the bytes-v1
// list-set ABI with a trailing GC write-barrier slot pointer.
// Osty: mirCallVoidListBytesV1SetWithBarrierLine
func mirCallVoidListBytesV1SetWithBarrierLine(sym, listReg, idxReg, slot, size, barrier string) string {
	return mirCallVoidLine(sym, "ptr "+listReg+", i64 "+idxReg+", ptr "+slot+", i64 "+size+", ptr "+barrier)
}

// mirCallVoidLikeRuntimeNoArgsLine — synonym of mirCallVoidNoArgsLine
// for the GC entry hook emission sites.
// Osty: mirCallVoidLikeRuntimeNoArgsLine
func mirCallVoidLikeRuntimeNoArgsLine(sym string) string {
	return mirCallVoidNoArgsLine(sym)
}

// mirSpillToBytesV1Lines renders the spill+sizeof preamble before a
// bytes-v1 runtime call: alloca + store + GEP-null + ptrtoint.
// Osty: mirSpillToBytesV1Lines
func mirSpillToBytesV1Lines(slot, slotTy, val, gepReg, sizeReg string) string {
	return mirAllocaSpillStoreLine(slot, slotTy, val) + mirSizeOfLines(gepReg, sizeReg, slotTy)
}

// mirRuntimeDeclareListBytesV1SetLine renders the canonical declare
// shape for the bytes-v1 list-set runtime ABI.
// Osty: mirRuntimeDeclareListBytesV1SetLine
func mirRuntimeDeclareListBytesV1SetLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, i64, ptr, i64, ptr")
}

// mirRuntimeDeclareListBytesV1GetLine renders the canonical declare
// shape for the bytes-v1 list-get runtime ABI.
// Osty: mirRuntimeDeclareListBytesV1GetLine
func mirRuntimeDeclareListBytesV1GetLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, i64, ptr, i64")
}

// mirRuntimeDeclareListPushBytesV1Line renders the canonical declare
// shape for the bytes-v1 list-push runtime ABI.
// Osty: mirRuntimeDeclareListPushBytesV1Line
func mirRuntimeDeclareListPushBytesV1Line(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, ptr, i64")
}

// mirRuntimeDeclareMapInsertLine renders the canonical declare shape
// for the runtime map-insert ABI.
// Osty: mirRuntimeDeclareMapInsertLine
func mirRuntimeDeclareMapInsertLine(sym, keyLLVM string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, "+keyLLVM+", ptr")
}

// mirRuntimeDeclareMapGetLine renders the canonical declare shape
// for the runtime map-probe ABI.
// Osty: mirRuntimeDeclareMapGetLine
func mirRuntimeDeclareMapGetLine(sym, keyLLVM string) string {
	return mirRuntimeDeclareLine("i1", sym, "ptr, "+keyLLVM+", ptr")
}

// mirRuntimeDeclareMapIncrLine renders the canonical declare shape
// for the fused map-incr runtime ABI.
// Osty: mirRuntimeDeclareMapIncrLine
func mirRuntimeDeclareMapIncrLine(sym, keyLLVM string) string {
	return mirRuntimeDeclareLine("i64", sym, "ptr, "+keyLLVM+", i64")
}

// mirRuntimeDeclareMapContainsLine renders the canonical declare
// shape for the runtime map-contains ABI.
// Osty: mirRuntimeDeclareMapContainsLine
func mirRuntimeDeclareMapContainsLine(sym, keyLLVM string) string {
	return mirRuntimeDeclareLine("i1", sym, "ptr, "+keyLLVM)
}

// mirRuntimeDeclareMapRemoveLine renders the canonical declare shape
// for the runtime map-remove ABI.
// Osty: mirRuntimeDeclareMapRemoveLine
func mirRuntimeDeclareMapRemoveLine(sym, keyLLVM string) string {
	return mirRuntimeDeclareLine("i1", sym, "ptr, "+keyLLVM)
}

// mirRuntimeDeclareListSetSimpleLine renders the canonical declare
// shape for the typed-element list-set runtime ABI.
// Osty: mirRuntimeDeclareListSetSimpleLine
func mirRuntimeDeclareListSetSimpleLine(sym, elemLLVM string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, i64, "+elemLLVM)
}

// §3 runtime-declare canonical-shape builders — drain the
// `"declare <ret> @"+sym+"(<args>)"` string-concat pattern at every
// call site in `mir_generator.go`. Each helper covers one common
// (ret, args) shape.

// mirRuntimeDeclarePrintf returns `declare i32 @printf(ptr, ...)`.
// Osty: mirRuntimeDeclarePrintf
func mirRuntimeDeclarePrintf() string {
	return "declare i32 @printf(ptr, ...)"
}

// mirRuntimeDeclareExit returns `declare void @exit(i32)`.
// Osty: mirRuntimeDeclareExit
func mirRuntimeDeclareExit() string {
	return "declare void @exit(i32)"
}

// mirRuntimeDeclarePtrFromPtrLine renders `declare ptr @<sym>(ptr)`.
// Osty: mirRuntimeDeclarePtrFromPtrLine
func mirRuntimeDeclarePtrFromPtrLine(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "ptr")
}

// mirRuntimeDeclareI1FromPtrLine renders `declare i1 @<sym>(ptr)`.
// Osty: mirRuntimeDeclareI1FromPtrLine
func mirRuntimeDeclareI1FromPtrLine(sym string) string {
	return mirRuntimeDeclareLine("i1", sym, "ptr")
}

// mirRuntimeDeclareVoidFromPtrLine renders `declare void @<sym>(ptr)`.
// Osty: mirRuntimeDeclareVoidFromPtrLine
func mirRuntimeDeclareVoidFromPtrLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr")
}

// mirRuntimeDeclareI64FromPtrLine renders `declare i64 @<sym>(ptr)`.
// Osty: mirRuntimeDeclareI64FromPtrLine
func mirRuntimeDeclareI64FromPtrLine(sym string) string {
	return mirRuntimeDeclareLine("i64", sym, "ptr")
}

// mirRuntimeDeclarePtrFromScalarLine renders `declare ptr @<sym>(<scalar>)`.
// Osty: mirRuntimeDeclarePtrFromScalarLine
func mirRuntimeDeclarePtrFromScalarLine(sym, scalarLLVM string) string {
	return mirRuntimeDeclareLine("ptr", sym, scalarLLVM)
}

// mirRuntimeDeclareI64NoArgsLine renders `declare i64 @<sym>()`.
// Osty: mirRuntimeDeclareI64NoArgsLine
func mirRuntimeDeclareI64NoArgsLine(sym string) string {
	return mirRuntimeDeclareLine("i64", sym, "")
}

// mirRuntimeDeclareVoidI32Line renders `declare void @<sym>(i32)`.
// Osty: mirRuntimeDeclareVoidI32Line
func mirRuntimeDeclareVoidI32Line(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "i32")
}

// mirRuntimeDeclareSafepointV1 returns the GC safepoint decl shape.
// Osty: mirRuntimeDeclareSafepointV1
func mirRuntimeDeclareSafepointV1() string {
	return "declare void @osty.gc.safepoint_v1(i64, ptr, i64)"
}

// mirRuntimeDeclareGcAllocV1 returns the GC allocator decl shape.
// Osty: mirRuntimeDeclareGcAllocV1
func mirRuntimeDeclareGcAllocV1() string {
	return "declare ptr @osty.gc.alloc_v1(i64, i64, ptr)"
}

// mirRuntimeDeclareStringConcat returns the String concat decl shape.
// Osty: mirRuntimeDeclareStringConcat
func mirRuntimeDeclareStringConcat() string {
	return "declare ptr @osty_rt_strings_Concat(ptr, ptr)"
}

// §6 Some-aggregate builders — repeating the Some(payload) two-
// insertvalue construction.

// mirSomeAggregateLines renders the canonical Some(payload) two-
// insertvalue pair into one block.
// Osty: mirSomeAggregateLines
func mirSomeAggregateLines(taggedReg, filledReg, optLLVM, payloadI64 string) string {
	return mirInsertValueAggLine(taggedReg, optLLVM, "undef", "i64", "1", "0") +
		mirInsertValueAggLine(filledReg, optLLVM, taggedReg, "i64", payloadI64, "1")
}

// mirSomeStoreLines renders Some-aggregate construction + store.
// Osty: mirSomeStoreLines
func mirSomeStoreLines(taggedReg, filledReg, optLLVM, payloadI64, destSlot string) string {
	return mirSomeAggregateLines(taggedReg, filledReg, optLLVM, payloadI64) +
		mirStoreLine(optLLVM, filledReg, destSlot)
}

// mirSomeStoreThenJumpLines renders Some-arm body: aggregate +
// store + br to join label.
// Osty: mirSomeStoreThenJumpLines
func mirSomeStoreThenJumpLines(taggedReg, filledReg, optLLVM, payloadI64, destSlot, endLabel string) string {
	return mirSomeStoreLines(taggedReg, filledReg, optLLVM, payloadI64, destSlot) +
		mirBrUncondLine(endLabel)
}

// mirNoneStoreThenJumpLines renders None-arm body: store
// zeroinitializer + br to join label.
// Osty: mirNoneStoreThenJumpLines
func mirNoneStoreThenJumpLines(optLLVM, destSlot, endLabel string) string {
	return mirStoreZeroinitLine(optLLVM, destSlot) + mirBrUncondLine(endLabel)
}

// mirICmpSltI64Line renders the loop-bound check `icmp slt i64`.
// Osty: mirICmpSltI64Line
func mirICmpSltI64Line(reg, lhs, rhs string) string {
	return mirICmpLine(reg, "slt", "i64", lhs, rhs)
}

// mirICmpSgeI64Line renders the lower-bound check `icmp sge i64`.
// Osty: mirICmpSgeI64Line
func mirICmpSgeI64Line(reg, lhs, rhs string) string {
	return mirICmpLine(reg, "sge", "i64", lhs, rhs)
}

// mirLinearScanLoopHeadLines renders the loop-head block of the
// linear-scan idiom: label + load + slt + cond-br.
// Osty: mirLinearScanLoopHeadLines
func mirLinearScanLoopHeadLines(headLabel, iReg, iSlot, cont, lenReg, bodyLabel, endLabel string) string {
	return mirLabelLine(headLabel) +
		mirLoadLine(iReg, "i64", iSlot) +
		mirICmpSltI64Line(cont, iReg, lenReg) +
		mirBrCondLine(cont, bodyLabel, endLabel)
}

// mirLinearScanLoopTailLines renders the loop-tail block: cont
// label + add + store + jump to head.
// Osty: mirLinearScanLoopTailLines
func mirLinearScanLoopTailLines(contLabel, nextReg, iReg, iSlot, headLabel string) string {
	return mirLabelLine(contLabel) +
		mirAddI64Line(nextReg, iReg, "1") +
		mirStoreLine("i64", nextReg, iSlot) +
		mirBrUncondLine(headLabel)
}

// mirNoneAggregateLines renders the canonical None two-insertvalue
// construction.
// Osty: mirNoneAggregateLines
func mirNoneAggregateLines(stepReg, valueReg, optLLVM string) string {
	return mirInsertValueAggLine(stepReg, optLLVM, "undef", "i64", "0", "0") +
		mirInsertValueAggLine(valueReg, optLLVM, stepReg, "i64", "0", "1")
}

// mirOkAggregateLines renders the canonical Ok(payload) two-
// insertvalue construction.
// Osty: mirOkAggregateLines
func mirOkAggregateLines(stepReg, valueReg, resultLLVM, payloadI64 string) string {
	return mirInsertValueAggLine(stepReg, resultLLVM, "undef", "i64", "1", "0") +
		mirInsertValueAggLine(valueReg, resultLLVM, stepReg, "i64", payloadI64, "1")
}

// mirErrAggregateLines renders the canonical Err(payload) two-
// insertvalue construction.
// Osty: mirErrAggregateLines
func mirErrAggregateLines(stepReg, valueReg, resultLLVM, payloadI64 string) string {
	return mirInsertValueAggLine(stepReg, resultLLVM, "undef", "i64", "0", "0") +
		mirInsertValueAggLine(valueReg, resultLLVM, stepReg, "i64", payloadI64, "1")
}

// mirCallVoidI64TagAndPtrLine renders the safepoint chunk-call
// shape `call void @<sym>(i64, ptr, i64)`.
// Osty: mirCallVoidI64TagAndPtrLine
func mirCallVoidI64TagAndPtrLine(sym, tag, slot, count string) string {
	return mirCallVoidLine(sym, "i64 "+tag+", ptr "+slot+", i64 "+count)
}

// mirAllocaI64ZeroSlot renders alloca i64 + store i64 0.
// Osty: mirAllocaI64ZeroSlot
func mirAllocaI64ZeroSlot(slot string) string {
	return mirAllocaWithStoreLine(slot, "i64", "0")
}

// mirAllocaI1FalseSlot renders alloca i1 + store i1 false.
// Osty: mirAllocaI1FalseSlot
func mirAllocaI1FalseSlot(slot string) string {
	return mirAllocaWithStoreLine(slot, "i1", "false")
}

// mirStoreI1TrueLine renders `store i1 true, ptr <slot>`.
// Osty: mirStoreI1TrueLine
func mirStoreI1TrueLine(slot string) string {
	return mirStoreLine("i1", "true", slot)
}

// §3 runtime-declare canonical-shape builders (continued).

// mirRuntimeDeclarePtrFromTwoPtrLine renders `declare ptr @<sym>(ptr, ptr)`.
// Osty: mirRuntimeDeclarePtrFromTwoPtrLine
func mirRuntimeDeclarePtrFromTwoPtrLine(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "ptr, ptr")
}

// mirRuntimeDeclareI64FromTwoPtrLine renders `declare i64 @<sym>(ptr, ptr)`.
// Osty: mirRuntimeDeclareI64FromTwoPtrLine
func mirRuntimeDeclareI64FromTwoPtrLine(sym string) string {
	return mirRuntimeDeclareLine("i64", sym, "ptr, ptr")
}

// mirRuntimeDeclareI1FromTwoPtrLine renders `declare i1 @<sym>(ptr, ptr)`.
// Osty: mirRuntimeDeclareI1FromTwoPtrLine
func mirRuntimeDeclareI1FromTwoPtrLine(sym string) string {
	return mirRuntimeDeclareLine("i1", sym, "ptr, ptr")
}

// mirRuntimeDeclarePtrFromPtrI64I64Line renders `declare ptr @<sym>(ptr, i64, i64)`.
// Osty: mirRuntimeDeclarePtrFromPtrI64I64Line
func mirRuntimeDeclarePtrFromPtrI64I64Line(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "ptr, i64, i64")
}

// mirRuntimeDeclarePtrFromThreePtrLine renders `declare ptr @<sym>(ptr, ptr, ptr)`.
// Osty: mirRuntimeDeclarePtrFromThreePtrLine
func mirRuntimeDeclarePtrFromThreePtrLine(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "ptr, ptr, ptr")
}

// mirRuntimeDeclarePtrFromPtrPtrI64Line renders `declare ptr @<sym>(ptr, ptr, i64)`.
// Osty: mirRuntimeDeclarePtrFromPtrPtrI64Line
func mirRuntimeDeclarePtrFromPtrPtrI64Line(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "ptr, ptr, i64")
}

// mirRuntimeDeclarePtrFromPtrI64PtrLine renders `declare ptr @<sym>(ptr, i64, ptr)`.
// Osty: mirRuntimeDeclarePtrFromPtrI64PtrLine
func mirRuntimeDeclarePtrFromPtrI64PtrLine(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "ptr, i64, ptr")
}

// mirRuntimeDeclarePtrFromPtrI64Line renders `declare ptr @<sym>(ptr, i64)`.
// Osty: mirRuntimeDeclarePtrFromPtrI64Line
func mirRuntimeDeclarePtrFromPtrI64Line(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "ptr, i64")
}

// mirRuntimeDeclarePtrFromI64Line renders `declare ptr @<sym>(i64)`.
// Osty: mirRuntimeDeclarePtrFromI64Line
func mirRuntimeDeclarePtrFromI64Line(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "i64")
}

// mirRuntimeDeclarePtrFromI64PtrLine renders `declare ptr @<sym>(i64, ptr)`.
// Osty: mirRuntimeDeclarePtrFromI64PtrLine
func mirRuntimeDeclarePtrFromI64PtrLine(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "i64, ptr")
}

// mirRuntimeDeclareEnumLayoutFromPtrLine renders `declare { i64, i64 } @<sym>(ptr)`.
// Osty: mirRuntimeDeclareEnumLayoutFromPtrLine
func mirRuntimeDeclareEnumLayoutFromPtrLine(sym string) string {
	return mirRuntimeDeclareLine("{ i64, i64 }", sym, "ptr")
}

// mirRuntimeDeclareEnumLayoutNoArgsLine renders `declare { i64, i64 } @<sym>()`.
// Osty: mirRuntimeDeclareEnumLayoutNoArgsLine
func mirRuntimeDeclareEnumLayoutNoArgsLine(sym string) string {
	return mirRuntimeDeclareLine("{ i64, i64 }", sym, "")
}

// mirRuntimeDeclareBytesV1GetLine renders the bytes-v1 list-get
// runtime decl `void @<sym>(ptr, i64, ptr, i64)`.
// Osty: mirRuntimeDeclareBytesV1GetLine
func mirRuntimeDeclareBytesV1GetLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, i64, ptr, i64")
}

// mirRuntimeDeclareBytesV1PushLine renders the bytes-v1 list-push
// runtime decl `void @<sym>(ptr, ptr, i64)`.
// Osty: mirRuntimeDeclareBytesV1PushLine
func mirRuntimeDeclareBytesV1PushLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, ptr, i64")
}

// mirRuntimeDeclareBytesV1SetWithBarrierLine renders
// `declare void @<sym>(ptr, i64, ptr, i64, ptr)`.
// Osty: mirRuntimeDeclareBytesV1SetWithBarrierLine
func mirRuntimeDeclareBytesV1SetWithBarrierLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, i64, ptr, i64, ptr")
}

// mirRuntimeDeclareThreePtrVoidLine renders `declare void @<sym>(ptr, ptr, ptr)`.
// Osty: mirRuntimeDeclareThreePtrVoidLine
func mirRuntimeDeclareThreePtrVoidLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, ptr, ptr")
}

// mirRuntimeDeclareTaskGroupSplitLine renders the task-group spawn
// 5-arg ABI `void @<sym>(ptr, ptr, ptr, i64, ptr)`.
// Osty: mirRuntimeDeclareTaskGroupSplitLine
func mirRuntimeDeclareTaskGroupSplitLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, ptr, ptr, i64, ptr")
}

// §6 list-pop / list-front read-projection helpers.

// mirSubI64MinusOneLine renders `<reg> = sub i64 <lenReg>, 1`.
// Osty: mirSubI64MinusOneLine
func mirSubI64MinusOneLine(reg, lenReg string) string {
	return mirSubI64Line(reg, lenReg, "1")
}

// mirAddI64PlusOneLine renders `<reg> = add i64 <iReg>, 1`.
// Osty: mirAddI64PlusOneLine
func mirAddI64PlusOneLine(reg, iReg string) string {
	return mirAddI64Line(reg, iReg, "1")
}

// mirLenGuardLines renders the canonical "is non-empty?" preamble
// used by every list intrinsic that returns Option<T>.
// Osty: mirLenGuardLines
func mirLenGuardLines(lenReg, isEmpty, lenSym, listReg string) string {
	return mirCallValueLine(lenReg, "i64", lenSym, "ptr "+listReg) +
		mirICmpEqI64Line(isEmpty, lenReg, "0")
}

// mirCallVoidPtrLine renders `call void @<sym>(ptr <ptr>)`.
// Osty: mirCallVoidPtrLine
func mirCallVoidPtrLine(sym, ptr string) string {
	return mirCallVoidLine(sym, "ptr "+ptr)
}

// mirCallValueI64FromPtrLine renders `<reg> = call i64 @<sym>(ptr <ptr>)`.
// Osty: mirCallValueI64FromPtrLine
func mirCallValueI64FromPtrLine(reg, sym, ptr string) string {
	return mirCallValueLine(reg, "i64", sym, "ptr "+ptr)
}

// mirCallValuePtrFromPtrLine renders `<reg> = call ptr @<sym>(ptr <ptr>)`.
// Osty: mirCallValuePtrFromPtrLine
func mirCallValuePtrFromPtrLine(reg, sym, ptr string) string {
	return mirCallValueLine(reg, "ptr", sym, "ptr "+ptr)
}

// mirCallValueI1FromPtrLine renders `<reg> = call i1 @<sym>(ptr <ptr>)`.
// Osty: mirCallValueI1FromPtrLine
func mirCallValueI1FromPtrLine(reg, sym, ptr string) string {
	return mirCallValueLine(reg, "i1", sym, "ptr "+ptr)
}

// mirRuntimeDeclareI8FromPtrI64Line renders `declare i8 @<sym>(ptr, i64)`.
// Osty: mirRuntimeDeclareI8FromPtrI64Line
func mirRuntimeDeclareI8FromPtrI64Line(sym string) string {
	return mirRuntimeDeclareLine("i8", sym, "ptr, i64")
}

// mirCallValueI8FromPtrI64Line renders `<reg> = call i8 @<sym>(ptr <ptr>, i64 <idx>)`.
// Osty: mirCallValueI8FromPtrI64Line
func mirCallValueI8FromPtrI64Line(reg, sym, ptr, idx string) string {
	return mirCallValueLine(reg, "i8", sym, "ptr "+ptr+", i64 "+idx)
}

// mirCallValueElemFromPtrI64Line renders typed-element list-get runtime call.
// Osty: mirCallValueElemFromPtrI64Line
func mirCallValueElemFromPtrI64Line(reg, elemLLVM, sym, ptr, idx string) string {
	return mirCallValueLine(reg, elemLLVM, sym, "ptr "+ptr+", i64 "+idx)
}

// §6 abort-and-exit shape builders.

// mirAbortPrintfExitLines renders printf+exit+unreachable+nextLabel.
// Osty: mirAbortPrintfExitLines
func mirAbortPrintfExitLines(fmtSym, messagePtr, nextLabel string) string {
	return mirCallVarargPrintfPathLine(fmtSym, messagePtr) +
		mirCallExitLine("1") +
		mirUnreachableLine() +
		mirLabelLine(nextLabel)
}

// mirBranchToErrorTrapLines renders `br i1 <isErr> + errLabel:`.
// Osty: mirBranchToErrorTrapLines
func mirBranchToErrorTrapLines(isErr, errLabel, okLabel string) string {
	return mirBrCondLine(isErr, errLabel, okLabel) + mirLabelLine(errLabel)
}

// mirNoneBranchLines renders Option-miss branch (label + zeroinit + br).
// Osty: mirNoneBranchLines
func mirNoneBranchLines(noneLabel, optLLVM, destSlot, endLabel string) string {
	return mirLabelLine(noneLabel) + mirNoneStoreThenJumpLines(optLLVM, destSlot, endLabel)
}

// mirSomeBranchLines renders Option-hit branch (label + Some + store + br).
// Osty: mirSomeBranchLines
func mirSomeBranchLines(someLabel, taggedReg, filledReg, optLLVM, payloadI64, destSlot, endLabel string) string {
	return mirLabelLine(someLabel) + mirSomeStoreThenJumpLines(taggedReg, filledReg, optLLVM, payloadI64, destSlot, endLabel)
}

// mirSomeNoneJoinLines renders the convergence label.
// Osty: mirSomeNoneJoinLines
func mirSomeNoneJoinLines(endLabel string) string {
	return mirLabelLine(endLabel)
}

// mirCallExitOneLine returns canonical `call void @exit(i32 1)`.
// Osty: mirCallExitOneLine
func mirCallExitOneLine() string {
	return mirCallExitLine("1")
}

// mirCallExitZeroLine returns canonical `call void @exit(i32 0)`.
// Osty: mirCallExitZeroLine
func mirCallExitZeroLine() string {
	return mirCallExitLine("0")
}

// mirAbortBlockLines renders the full abort block.
// Osty: mirAbortBlockLines
func mirAbortBlockLines(errLabel, fmtSym, messagePtr, nextLabel string) string {
	return mirLabelLine(errLabel) + mirAbortPrintfExitLines(fmtSym, messagePtr, nextLabel)
}

// mirCallI64FromTwoPtrLine renders `<reg> = call i64 @<sym>(ptr, ptr)`.
// Osty: mirCallI64FromTwoPtrLine
func mirCallI64FromTwoPtrLine(reg, sym, left, right string) string {
	return mirCallValueLine(reg, "i64", sym, "ptr "+left+", ptr "+right)
}

// mirCallI1FromTwoPtrLine renders `<reg> = call i1 @<sym>(ptr, ptr)`.
// Osty: mirCallI1FromTwoPtrLine
func mirCallI1FromTwoPtrLine(reg, sym, left, right string) string {
	return mirCallValueLine(reg, "i1", sym, "ptr "+left+", ptr "+right)
}

// mirCallPtrFromTwoPtrLine renders `<reg> = call ptr @<sym>(ptr, ptr)`.
// Osty: mirCallPtrFromTwoPtrLine
func mirCallPtrFromTwoPtrLine(reg, sym, left, right string) string {
	return mirCallValueLine(reg, "ptr", sym, "ptr "+left+", ptr "+right)
}

// mirCallVoidFromTwoPtrLine renders `call void @<sym>(ptr, ptr)`.
// Osty: mirCallVoidFromTwoPtrLine
func mirCallVoidFromTwoPtrLine(sym, left, right string) string {
	return mirCallVoidLine(sym, "ptr "+left+", ptr "+right)
}

// mirCallVoidFromThreePtrLine renders `call void @<sym>(ptr, ptr, ptr)`.
// Osty: mirCallVoidFromThreePtrLine
func mirCallVoidFromThreePtrLine(sym, a, b, c string) string {
	return mirCallVoidLine(sym, "ptr "+a+", ptr "+b+", ptr "+c)
}

// mirInsertValueI64IndexLine renders `<reg> = insertvalue <aggTy> <base>, i64 <val>, <idx>`.
// Osty: mirInsertValueI64IndexLine
func mirInsertValueI64IndexLine(reg, aggTy, baseVal, val, idxDigits string) string {
	return mirInsertValueAggLine(reg, aggTy, baseVal, "i64", val, idxDigits)
}

// mirExtractValueI64IndexLine renders `<reg> = extractvalue <aggTy> <agg>, <idx>`.
// Osty: mirExtractValueI64IndexLine
func mirExtractValueI64IndexLine(reg, aggTy, aggVal, idxDigits string) string {
	return "  " + reg + " = extractvalue " + aggTy + " " + aggVal + ", " + idxDigits + "\n"
}

// mirCallVoidI64Line renders `call void @<sym>(i64 <arg>)`.
// Osty: mirCallVoidI64Line
func mirCallVoidI64Line(sym, arg string) string {
	return mirCallVoidLine(sym, "i64 "+arg)
}

// mirCallVoidI32Line renders `call void @<sym>(i32 <arg>)`.
// Osty: mirCallVoidI32Line
func mirCallVoidI32Line(sym, arg string) string {
	return mirCallVoidLine(sym, "i32 "+arg)
}

// mirGEPInboundsI64IdxLine renders i64-indexed inbounds GEP.
// Osty: mirGEPInboundsI64IdxLine
func mirGEPInboundsI64IdxLine(reg, elemTy, basePtr, idx string) string {
	return "  " + reg + " = getelementptr inbounds " + elemTy + ", ptr " + basePtr + ", i64 " + idx + "\n"
}

// mirZExtToI64Line / mirSExtToI64Line / mirBitcastToI64Line / mirPtrToInt64Line — i64-payload widen specializations.
// Osty: mirZExtToI64Line
func mirZExtToI64Line(reg, fromTy, val string) string { return mirZExtLine(reg, fromTy, val, "i64") }

// Osty: mirSExtToI64Line
func mirSExtToI64Line(reg, fromTy, val string) string { return mirSExtLine(reg, fromTy, val, "i64") }

// Osty: mirBitcastToI64Line
func mirBitcastToI64Line(reg, val string) string { return mirBitcastLine(reg, "double", val, "i64") }

// Osty: mirPtrToInt64Line
func mirPtrToInt64Line(reg, val string) string { return mirPtrToIntLine(reg, val, "i64") }

// mirCallStringConcatLine specialisation for osty_rt_strings_Concat.
// Osty: mirCallStringConcatLine
func mirCallStringConcatLine(reg, left, right string) string {
	return mirCallPtrFromTwoPtrLine(reg, "osty_rt_strings_Concat", left, right)
}

// mirCallStringEqualLine specialisation for osty_rt_strings_Equal.
// Osty: mirCallStringEqualLine
func mirCallStringEqualLine(reg, left, right string) string {
	return mirCallI1FromTwoPtrLine(reg, "osty_rt_strings_Equal", left, right)
}

// mirCallStringCompareLine specialisation for osty_rt_strings_Compare.
// Osty: mirCallStringCompareLine
func mirCallStringCompareLine(reg, left, right string) string {
	return mirCallI64FromTwoPtrLine(reg, "osty_rt_strings_Compare", left, right)
}

// mirCallStringSubstringLine specialisation for osty_rt_strings_Substring.
// Osty: mirCallStringSubstringLine
func mirCallStringSubstringLine(reg, src, startIdx, endIdx string) string {
	return mirCallValueLine(reg, "ptr", "osty_rt_strings_Substring", "ptr "+src+", i64 "+startIdx+", i64 "+endIdx)
}

// mirCallListNewLine renders osty_rt_list_new() call.
// Osty: mirCallListNewLine
func mirCallListNewLine(reg string) string {
	return mirCallValueNoArgsLine(reg, "ptr", "osty_rt_list_new")
}

// mirCallMapNewLine renders osty_rt_map_new(...) call.
// Osty: mirCallMapNewLine
func mirCallMapNewLine(reg, keyKind, valKind, valSize string) string {
	return mirCallValueLine(reg, "ptr", "osty_rt_map_new", "i64 "+keyKind+", i64 "+valKind+", i64 "+valSize+", ptr null")
}

// mirCallSetNewLine renders osty_rt_set_new(elemKind) call.
// Osty: mirCallSetNewLine
func mirCallSetNewLine(reg, elemKind string) string {
	return mirCallValueLine(reg, "ptr", "osty_rt_set_new", "i64 "+elemKind)
}

// mirSizeOf*Line — canonical sizeof literals.
// Osty: mirSizeOfDoubleLine
func mirSizeOfDoubleLine() string { return "8" }

// Osty: mirSizeOfI64Line
func mirSizeOfI64Line() string { return "8" }

// Osty: mirSizeOfI32Line
func mirSizeOfI32Line() string { return "4" }

// Osty: mirSizeOfI8Line
func mirSizeOfI8Line() string { return "1" }

// Osty: mirSizeOfPtrLine
func mirSizeOfPtrLine() string { return "8" }

// Osty: mirSizeOfI1Line
func mirSizeOfI1Line() string { return "1" }

// §6 concurrency-shape and additional emit-shape builders.

// mirCallVoidPtrI64Line — call void @<sym>(ptr, i64).
// Osty: mirCallVoidPtrI64Line
func mirCallVoidPtrI64Line(sym, ptr, idx string) string {
	return mirCallVoidLine(sym, "ptr "+ptr+", i64 "+idx)
}

// mirCallValuePtrI64Line — typed-return sibling.
// Osty: mirCallValuePtrI64Line
func mirCallValuePtrI64Line(reg, retTy, sym, ptr, idx string) string {
	return mirCallValueLine(reg, retTy, sym, "ptr "+ptr+", i64 "+idx)
}

// mirCallVoidSelectSendLine — typed select-send call shape.
// Osty: mirCallVoidSelectSendLine
func mirCallVoidSelectSendLine(sym, builderReg, chReg, elemLLVM, valReg, armReg string) string {
	return mirCallVoidLine(sym, "ptr "+builderReg+", ptr "+chReg+", "+elemLLVM+" "+valReg+", ptr "+armReg)
}

// mirCallVoidSelectSendBytesLine — bytes-v1 select-send call.
// Osty: mirCallVoidSelectSendBytesLine
func mirCallVoidSelectSendBytesLine(sym, builderReg, chReg, slot, size, armReg string) string {
	return mirCallVoidLine(sym, "ptr "+builderReg+", ptr "+chReg+", ptr "+slot+", i64 "+size+", ptr "+armReg)
}

// mirRuntimeDeclareSelectSendLine — declare void @<sym>(ptr, ptr, <elem>, ptr).
// Osty: mirRuntimeDeclareSelectSendLine
func mirRuntimeDeclareSelectSendLine(sym, elemLLVM string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, ptr, "+elemLLVM+", ptr")
}

// mirRuntimeDeclareSelectSendBytesLine — bytes-v1 select-send decl.
// Osty: mirRuntimeDeclareSelectSendBytesLine
func mirRuntimeDeclareSelectSendBytesLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, ptr, ptr, i64, ptr")
}

// mirCallVoidArgValueLine — call void @<sym>(<argLLVM> <val>).
// Osty: mirCallVoidArgValueLine
func mirCallVoidArgValueLine(sym, argLLVM, val string) string {
	return mirCallVoidLine(sym, argLLVM+" "+val)
}

// mirRuntimeDeclareVoidSingleArgLine — declare void @<sym>(<argLLVM>).
// Osty: mirRuntimeDeclareVoidSingleArgLine
func mirRuntimeDeclareVoidSingleArgLine(sym, argLLVM string) string {
	return mirRuntimeDeclareLine("void", sym, argLLVM)
}

// mirCallValueListLenLine / MapLenLine / SetLenLine — canonical
// container-len call specialisations.
// Osty: mirCallValueListLenLine
func mirCallValueListLenLine(reg, listReg string) string {
	return mirCallValueI64FromPtrLine(reg, "osty_rt_list_len", listReg)
}

// Osty: mirCallValueMapLenLine
func mirCallValueMapLenLine(reg, mapReg string) string {
	return mirCallValueI64FromPtrLine(reg, "osty_rt_map_len", mapReg)
}

// Osty: mirCallValueSetLenLine
func mirCallValueSetLenLine(reg, setReg string) string {
	return mirCallValueI64FromPtrLine(reg, "osty_rt_set_len", setReg)
}

// mirCallValueChanRecvLine — typed `{ i64, i64 }` chan-recv call.
// Osty: mirCallValueChanRecvLine
func mirCallValueChanRecvLine(reg, sym, chReg string) string {
	return mirCallValueLine(reg, "{ i64, i64 }", sym, "ptr "+chReg)
}

// mirCallValueCancelCheckLine — osty_rt_cancel_check_cancelled() call.
// Osty: mirCallValueCancelCheckLine
func mirCallValueCancelCheckLine(reg string) string {
	return mirCallValueNoArgsLine(reg, "{ i64, i64 }", "osty_rt_cancel_check_cancelled")
}

// mirCallValueCancelIsCancelledLine — osty_rt_cancel_is_cancelled() call.
// Osty: mirCallValueCancelIsCancelledLine
func mirCallValueCancelIsCancelledLine(reg string) string {
	return mirCallValueNoArgsLine(reg, "i1", "osty_rt_cancel_is_cancelled")
}

// mirSpillThenSizeOfLines — spill + sizeof preamble.
// Osty: mirSpillThenSizeOfLines
func mirSpillThenSizeOfLines(slot, ty, val, gepReg, sizeReg string) string {
	return mirAllocaSpillStoreLine(slot, ty, val) + mirSizeOfLines(gepReg, sizeReg, ty)
}

// mirCallValueListSortedLine — osty_rt_list_sorted_<elem>(list) call.
// Osty: mirCallValueListSortedLine
func mirCallValueListSortedLine(reg, sym, listReg string) string {
	return mirCallValuePtrFromPtrLine(reg, sym, listReg)
}

// mirCallValueMapKeysSortedLine — fused map.keys().sorted() call.
// Osty: mirCallValueMapKeysSortedLine
func mirCallValueMapKeysSortedLine(reg, sym, mapReg string) string {
	return mirCallValuePtrFromPtrLine(reg, sym, mapReg)
}

// mirCallVoidListPushTypedLine — typed-element list-push call.
// Osty: mirCallVoidListPushTypedLine
func mirCallVoidListPushTypedLine(sym, listReg, elemLLVM, valReg string) string {
	return mirCallVoidLine(sym, "ptr "+listReg+", "+elemLLVM+" "+valReg)
}

// mirRuntimeDeclareListPushTypedLine — typed-element list-push decl.
// Osty: mirRuntimeDeclareListPushTypedLine
func mirRuntimeDeclareListPushTypedLine(sym, elemLLVM string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, "+elemLLVM)
}

// mirCallVoidChanCloseLine — osty_rt_chan_close specialisation.
// Osty: mirCallVoidChanCloseLine
func mirCallVoidChanCloseLine(chReg string) string {
	return mirCallVoidPtrLine("osty_rt_chan_close", chReg)
}

// mirCallVoidCancelCancelLine — osty_rt_cancel_cancel call.
// Osty: mirCallVoidCancelCancelLine
func mirCallVoidCancelCancelLine() string {
	return mirCallVoidNoArgsLine("osty_rt_cancel_cancel")
}

// mirCallVoidYieldLine — osty_rt_task_yield call.
// Osty: mirCallVoidYieldLine
func mirCallVoidYieldLine() string {
	return mirCallVoidNoArgsLine("osty_rt_task_yield")
}

// mirCallValueListReversedLine — osty_rt_list_reversed allocator call.
// Osty: mirCallValueListReversedLine
func mirCallValueListReversedLine(reg, listReg string) string {
	return mirCallValuePtrFromPtrLine(reg, "osty_rt_list_reversed", listReg)
}

// mirCallVoidListReverseLine — in-place osty_rt_list_reverse call.
// Osty: mirCallVoidListReverseLine
func mirCallVoidListReverseLine(listReg string) string {
	return mirCallVoidPtrLine("osty_rt_list_reverse", listReg)
}

// mirCallVoidListClearLine / MapClearLine / SetClearLine.
// Osty: mirCallVoidListClearLine
func mirCallVoidListClearLine(listReg string) string {
	return mirCallVoidPtrLine("osty_rt_list_clear", listReg)
}

// Osty: mirCallVoidMapClearLine
func mirCallVoidMapClearLine(mapReg string) string {
	return mirCallVoidPtrLine("osty_rt_map_clear", mapReg)
}

// Osty: mirCallVoidSetClearLine
func mirCallVoidSetClearLine(setReg string) string {
	return mirCallVoidPtrLine("osty_rt_set_clear", setReg)
}

// mirCallVoidPopDiscardLine — osty_rt_list_pop_discard call.
// Osty: mirCallVoidPopDiscardLine
func mirCallVoidPopDiscardLine(listReg string) string {
	return mirCallVoidPtrLine("osty_rt_list_pop_discard", listReg)
}

// mirCallValueIsEmptyLine — generic is-empty probe.
// Osty: mirCallValueIsEmptyLine
func mirCallValueIsEmptyLine(reg, sym, handleReg string) string {
	return mirCallValueI1FromPtrLine(reg, sym, handleReg)
}

// mirCallI1MapKeyLine renders `<reg> = call i1 @<sym>(ptr <map>, <keyLLVM> <key>)`.
// Osty: mirCallI1MapKeyLine
func mirCallI1MapKeyLine(reg, sym, mapReg, keyLLVM, keyReg string) string {
	return mirCallValueLine(reg, "i1", sym, "ptr "+mapReg+", "+keyLLVM+" "+keyReg)
}

// mirCallI1SetElemLine renders `<reg> = call i1 @<sym>(ptr <set>, <elemLLVM> <elem>)`.
// Osty: mirCallI1SetElemLine
func mirCallI1SetElemLine(reg, sym, setReg, elemLLVM, elemReg string) string {
	return mirCallValueLine(reg, "i1", sym, "ptr "+setReg+", "+elemLLVM+" "+elemReg)
}

// mirCallVoidSetElemLine renders `call void @<sym>(ptr <set>, <elemLLVM> <elem>)`.
// Osty: mirCallVoidSetElemLine
func mirCallVoidSetElemLine(sym, setReg, elemLLVM, elemReg string) string {
	return mirCallVoidLine(sym, "ptr "+setReg+", "+elemLLVM+" "+elemReg)
}

// mirRuntimeDeclareI1FromPtrAndElemLine — declare i1 @<sym>(ptr, <elem>).
// Osty: mirRuntimeDeclareI1FromPtrAndElemLine
func mirRuntimeDeclareI1FromPtrAndElemLine(sym, elemLLVM string) string {
	return mirRuntimeDeclareLine("i1", sym, "ptr, "+elemLLVM)
}

// mirRuntimeDeclareVoidFromPtrAndElemLine — declare void @<sym>(ptr, <elem>).
// Osty: mirRuntimeDeclareVoidFromPtrAndElemLine
func mirRuntimeDeclareVoidFromPtrAndElemLine(sym, elemLLVM string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, "+elemLLVM)
}

// mirCallValueElemFromPtrAndElemLine — typed-return sibling of mirCallVoidSetElemLine.
// Osty: mirCallValueElemFromPtrAndElemLine
func mirCallValueElemFromPtrAndElemLine(reg, retTy, sym, handleReg, elemLLVM, elemReg string) string {
	return mirCallValueLine(reg, retTy, sym, "ptr "+handleReg+", "+elemLLVM+" "+elemReg)
}

// mirCallVoidChanSendLine — typed chan-send call.
// Osty: mirCallVoidChanSendLine
func mirCallVoidChanSendLine(sym, chReg, elemLLVM, valReg string) string {
	return mirCallVoidLine(sym, "ptr "+chReg+", "+elemLLVM+" "+valReg)
}

// mirRuntimeDeclareChanSendLine — declare void @<sym>(ptr, <elem>).
// Osty: mirRuntimeDeclareChanSendLine
func mirRuntimeDeclareChanSendLine(sym, elemLLVM string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, "+elemLLVM)
}

// mirCallVoidChanSendBytesLine — bytes-v1 chan-send call.
// Osty: mirCallVoidChanSendBytesLine
func mirCallVoidChanSendBytesLine(sym, chReg, slot, size string) string {
	return mirCallVoidLine(sym, "ptr "+chReg+", ptr "+slot+", i64 "+size)
}

// mirCallVoidIntrinsicArgsLine / mirCallValueIntrinsicArgsLine — re-exports.
// Osty: mirCallVoidIntrinsicArgsLine
func mirCallVoidIntrinsicArgsLine(sym, argList string) string {
	return mirCallVoidLine(sym, argList)
}

// Osty: mirCallValueIntrinsicArgsLine
func mirCallValueIntrinsicArgsLine(reg, retTy, sym, argList string) string {
	return mirCallValueLine(reg, retTy, sym, argList)
}

// mirCallGcAllocV1Line — osty.gc.alloc_v1 specialisation.
// Osty: mirCallGcAllocV1Line
func mirCallGcAllocV1Line(reg, kind, size, site string) string {
	return mirCallValueLine(reg, "ptr", "osty.gc.alloc_v1", "i64 "+kind+", i64 "+size+", ptr "+site)
}

// mirCallSafepointLine — osty.gc.safepoint_v1 specialisation.
// Osty: mirCallSafepointLine
func mirCallSafepointLine(tag, slots, count string) string {
	return mirCallVoidI64TagAndPtrLine("osty.gc.safepoint_v1", tag, slots, count)
}
