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
// builds the byte-by-byte string-equality switch the legacy emitter
// inlined directly into g.fnBuf. `litBytes` is the per-byte int view of
// the literal (caller converts via int(lit[i])); the !=  path appends
// one final `xor i1 ..., true` so callers always receive the post-
// negation register without tracking the op themselves.
//
// Implementation parity: every FreshLabel / Fresh call here matches the
// Go source (mir_generator.go: emitInlineStringEqLiteral) one-for-one
// so MirSeq.TempSeq advances by exactly the same amount the legacy
// stream did, keeping SSA / label numbering byte-stable across the
// port.
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

	ptrEq := s.Fresh()
	lines = append(lines, "  "+ptrEq+" = icmp eq ptr "+dynReg+", "+litSym+"\n")

	n := len(litBytes)
	byteLabels := make([]string, 0, n+1)
	for k := 0; k <= n; k++ {
		byteLabels = append(byteLabels, s.FreshLabel("streq.b"+strconv.Itoa(k)))
	}
	lines = append(lines,
		"  br i1 "+ptrEq+
			", label %"+matchLabel+
			", label %"+byteLabels[0]+"\n",
	)

	for i := 0; i <= n; i++ {
		lines = append(lines, byteLabels[i]+":\n")
		ptrReg := dynReg
		if i > 0 {
			ptrReg = s.Fresh()
			lines = append(lines,
				"  "+ptrReg+
					" = getelementptr inbounds i8, ptr "+dynReg+
					", i64 "+strconv.Itoa(i)+"\n",
			)
		}
		byteReg := s.Fresh()
		lines = append(lines, "  "+byteReg+" = load i8, ptr "+ptrReg+"\n")

		expected := 0
		if i < n {
			expected = litBytes[i]
		}
		matchReg := s.Fresh()
		lines = append(lines,
			"  "+matchReg+
				" = icmp eq i8 "+byteReg+
				", "+strconv.Itoa(expected)+"\n",
		)

		nextLabel := matchLabel
		if i < n {
			nextLabel = byteLabels[i+1]
		}
		lines = append(lines,
			"  br i1 "+matchReg+
				", label %"+nextLabel+
				", label %"+nomatchLabel+"\n",
		)
	}

	lines = append(lines, matchLabel+":\n  br label %"+doneLabel+"\n")
	lines = append(lines, nomatchLabel+":\n  br label %"+doneLabel+"\n")
	lines = append(lines, doneLabel+":\n")

	eq := s.Fresh()
	lines = append(lines,
		"  "+eq+
			" = phi i1 [true, %"+matchLabel+
			"], [false, %"+nomatchLabel+"]\n",
	)

	if opIsEq {
		return MirInlineStringEqResult{FinalReg: eq, Lines: lines}
	}
	neq := s.Fresh()
	lines = append(lines, "  "+neq+" = xor i1 "+eq+", true\n")
	return MirInlineStringEqResult{FinalReg: neq, Lines: lines}
}
