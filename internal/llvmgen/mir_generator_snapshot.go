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
		func() struct{} { parts = append(parts, mirFnAttrInlineHint()); return struct{}{} }()
	} else if inlineMode == 2 {
		// Osty: toolchain/mir_generator.osty:404:9
		func() struct{} { parts = append(parts, mirFnAttrAlwaysInline()); return struct{}{} }()
	} else if inlineMode == 3 {
		// Osty: toolchain/mir_generator.osty:406:9
		func() struct{} { parts = append(parts, mirFnAttrNoInline()); return struct{}{} }()
	}
	// Osty: toolchain/mir_generator.osty:408:5
	if hot {
		// Osty: toolchain/mir_generator.osty:409:9
		func() struct{} { parts = append(parts, mirFnAttrHot()); return struct{}{} }()
	}
	// Osty: toolchain/mir_generator.osty:411:5
	if cold {
		// Osty: toolchain/mir_generator.osty:412:9
		func() struct{} { parts = append(parts, mirFnAttrCold()); return struct{}{} }()
	}
	// Osty: toolchain/mir_generator.osty:416:5
	if pureFn {
		// Osty: toolchain/mir_generator.osty:417:9
		func() struct{} { parts = append(parts, mirFnAttrPure()); return struct{}{} }()
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

// §6 typed-element list runtime call shapes (continued).

// mirCallValueListGetTypedLine — synonym at IndexOf/Contains body.
// Osty: mirCallValueListGetTypedLine
func mirCallValueListGetTypedLine(reg, elemLLVM, sym, listReg, idxReg string) string {
	return mirCallValueElemFromPtrI64Line(reg, elemLLVM, sym, listReg, idxReg)
}

// mirCallValueListSlowGetLine — vector-list slow-path get.
// Osty: mirCallValueListSlowGetLine
func mirCallValueListSlowGetLine(reg, elemLLVM, slowSym, listReg, idxVal string) string {
	return mirCallValueElemFromPtrI64Line(reg, elemLLVM, slowSym, listReg, idxVal)
}

// §6 bench harness probe / clock / GC-counter call shapes.

// mirCallValueBenchTargetNsLine — bench --benchtime probe.
// Osty: mirCallValueBenchTargetNsLine
func mirCallValueBenchTargetNsLine(reg string) string {
	return mirCallValueNoArgsLine(reg, "i64", "osty_rt_bench_target_ns")
}

// mirCallValueBenchNowNanosLine — bench clock read.
// Osty: mirCallValueBenchNowNanosLine
func mirCallValueBenchNowNanosLine(reg string) string {
	return mirCallValueNoArgsLine(reg, "i64", "osty_rt_bench_now_nanos")
}

// mirCallValueGcDebugAllocatedBytesLine — GC odometer probe.
// Osty: mirCallValueGcDebugAllocatedBytesLine
func mirCallValueGcDebugAllocatedBytesLine(reg string) string {
	return mirCallValueNoArgsLine(reg, "i64", "osty_gc_debug_allocated_bytes_total")
}

// mirCallVoidOptionUnwrapNoneLine — Option.unwrap() panic helper.
// Osty: mirCallVoidOptionUnwrapNoneLine
func mirCallVoidOptionUnwrapNoneLine() string {
	return mirCallVoidNoArgsLine("osty_rt_option_unwrap_none")
}

// mirCallVoidResultUnwrapErrLine — Result.unwrap() panic helper.
// Osty: mirCallVoidResultUnwrapErrLine
func mirCallVoidResultUnwrapErrLine() string {
	return mirCallVoidNoArgsLine("osty_rt_result_unwrap_err")
}

// mirCallVoidExpectFailedLine — testing.expect* failure helper.
// Osty: mirCallVoidExpectFailedLine
func mirCallVoidExpectFailedLine() string {
	return mirCallVoidNoArgsLine("osty_rt_test_expect_failed")
}

// mirCallValueRuntimeProbe / Stmt — `{ i64, i64 }` runtime hooks.
// Osty: mirCallValueRuntimeProbe
func mirCallValueRuntimeProbe(reg, sym, argList string) string {
	return mirCallValueLine(reg, "{ i64, i64 }", sym, argList)
}

// Osty: mirCallStmtRuntimeProbe
func mirCallStmtRuntimeProbe(sym, argList string) string {
	return mirCallStmtLine("{ i64, i64 }", sym, argList)
}

// mirCallValueOpaqueLine / mirCallVoidOpaqueLine — ptr-returning generic call shapes.
// Osty: mirCallValueOpaqueLine
func mirCallValueOpaqueLine(reg, sym, argList string) string {
	return mirCallValueLine(reg, "ptr", sym, argList)
}

// Osty: mirCallVoidOpaqueLine
func mirCallVoidOpaqueLine(sym, argList string) string {
	return mirCallVoidLine(sym, argList)
}

// §6 LLVM-text comment / annotation builders.

// Osty: mirCommentBlockHeader
func mirCommentBlockHeader(text string) string {
	return "; ==== " + text + " ====\n"
}

// Osty: mirCommentSourceLine
func mirCommentSourceLine(loc string) string {
	return "  ; " + loc + "\n"
}

// Osty: mirSectionSeparator
func mirSectionSeparator() string {
	return "\n"
}

// §6 misc shape specialisations.

// Osty: mirIntegerWidenZExt{I8,I16,I32,I1}Line
func mirIntegerWidenZExtI8Line(reg, val string) string  { return mirZExtToI64Line(reg, "i8", val) }
func mirIntegerWidenZExtI16Line(reg, val string) string { return mirZExtToI64Line(reg, "i16", val) }
func mirIntegerWidenZExtI32Line(reg, val string) string { return mirZExtToI64Line(reg, "i32", val) }
func mirIntegerWidenZExtI1Line(reg, val string) string  { return mirZExtToI64Line(reg, "i1", val) }

// Osty: mirIntegerNarrowTruncI64{,ToI1,ToI8,ToI16,ToI32}Line
func mirIntegerNarrowTruncI64Line(reg, val, toTy string) string {
	return mirTruncLine(reg, "i64", val, toTy)
}
func mirIntegerNarrowTruncI64ToI1Line(reg, val string) string {
	return mirTruncLine(reg, "i64", val, "i1")
}
func mirIntegerNarrowTruncI64ToI8Line(reg, val string) string {
	return mirTruncLine(reg, "i64", val, "i8")
}
func mirIntegerNarrowTruncI64ToI16Line(reg, val string) string {
	return mirTruncLine(reg, "i64", val, "i16")
}
func mirIntegerNarrowTruncI64ToI32Line(reg, val string) string {
	return mirTruncLine(reg, "i64", val, "i32")
}

// Osty: mirBitcastI64ToDoubleLine / mirIntToPtrI64Line
func mirBitcastI64ToDoubleLine(reg, val string) string {
	return mirBitcastLine(reg, "i64", val, "double")
}
func mirIntToPtrI64Line(reg, val string) string { return mirIntToPtrLine(reg, val, "ptr") }

// §6 store / load specialisation builders.

// Osty: mirStoreI64Line / mirStoreI1Line / mirStorePtrTypedLine
func mirStoreI64Line(val, slot string) string      { return mirStoreLine("i64", val, slot) }
func mirStoreI1Line(val, slot string) string       { return mirStoreLine("i1", val, slot) }
func mirStorePtrTypedLine(val, slot string) string { return mirStorePtrLine(val, slot) }

// Osty: mirLoadI64Line / mirLoadI1Line / mirLoadPtrLine / mirLoadDoubleLine
func mirLoadI64Line(reg, ptr string) string    { return mirLoadLine(reg, "i64", ptr) }
func mirLoadI1Line(reg, ptr string) string     { return mirLoadLine(reg, "i1", ptr) }
func mirLoadPtrLine(reg, ptr string) string    { return mirLoadLine(reg, "ptr", ptr) }
func mirLoadDoubleLine(reg, ptr string) string { return mirLoadLine(reg, "double", ptr) }

// §6 GEP-with-suffix builders.

// Osty: mirGEPI64StrideLine / mirGEPDoubleStrideLine / mirGEPPtrStrideLine
func mirGEPI64StrideLine(reg, basePtr, idx string) string {
	return mirGEPInboundsI64IdxLine(reg, "i64", basePtr, idx)
}
func mirGEPDoubleStrideLine(reg, basePtr, idx string) string {
	return mirGEPInboundsI64IdxLine(reg, "double", basePtr, idx)
}
func mirGEPPtrStrideLine(reg, basePtr, idx string) string {
	return mirGEPInboundsI64IdxLine(reg, "ptr", basePtr, idx)
}

// §6 br / phi specialisation builders.

// Osty: mirBrUncondToHeadLine / mirBrUncondToEndLine
func mirBrUncondToHeadLine(headLabel string) string { return mirBrUncondLine(headLabel) }
func mirBrUncondToEndLine(endLabel string) string   { return mirBrUncondLine(endLabel) }

// Osty: mirPhiI64FromTwoLine / mirPhiPtrFromTwoLine / mirPhiI1FromTwoValuesLine / mirPhiDoubleFromTwoLine
func mirPhiI64FromTwoLine(reg, v1, l1, v2, l2 string) string {
	return mirPhiTwoLine(reg, "i64", v1, l1, v2, l2)
}
func mirPhiPtrFromTwoLine(reg, v1, l1, v2, l2 string) string {
	return mirPhiTwoLine(reg, "ptr", v1, l1, v2, l2)
}
func mirPhiI1FromTwoValuesLine(reg, v1, l1, v2, l2 string) string {
	return mirPhiTwoLine(reg, "i1", v1, l1, v2, l2)
}
func mirPhiDoubleFromTwoLine(reg, v1, l1, v2, l2 string) string {
	return mirPhiTwoLine(reg, "double", v1, l1, v2, l2)
}

// §6 select specialisation builders.

// Osty: mirSelectI64Line / mirSelectPtrLine / mirSelectI1Line / mirSelectDoubleLine
func mirSelectI64Line(reg, cond, l, r string) string { return mirSelectLine(reg, "i64", cond, l, r) }
func mirSelectPtrLine(reg, cond, l, r string) string { return mirSelectLine(reg, "ptr", cond, l, r) }
func mirSelectI1Line(reg, cond, l, r string) string  { return mirSelectLine(reg, "i1", cond, l, r) }
func mirSelectDoubleLine(reg, cond, l, r string) string {
	return mirSelectLine(reg, "double", cond, l, r)
}

// §6 compound conditional / boolean-helper builders.

// mirInBoundsLines — non-neg AND in-upper bounds check (3-line block).
// Osty: mirInBoundsLines
func mirInBoundsLines(nonNeg, inUpper, inBounds, idx, lenReg string) string {
	return mirICmpSgeI64Line(nonNeg, idx, "0") +
		mirICmpSltI64Line(inUpper, idx, lenReg) +
		mirAndI1Line(inBounds, nonNeg, inUpper)
}

// mirOutOfBoundsTrapLines — OOB-abort body (call+unreachable).
// Osty: mirOutOfBoundsTrapLines
func mirOutOfBoundsTrapLines(oobSym string) string {
	return mirCallVoidNoReturnNoArgsLine(oobSym) + mirUnreachableLine()
}

// §6 loop-counter increment / decrement specialisations.

// Osty: mirIncrementI64Line / mirDecrementI64Line
func mirIncrementI64Line(reg, iReg, delta string) string { return mirAddI64Line(reg, iReg, delta) }
func mirDecrementI64Line(reg, iReg, delta string) string { return mirSubI64Line(reg, iReg, delta) }

// §6 misc emitter shape specialisations.

// Osty: mirReturnI64Line / mirReturnPtrLine / mirReturnI1Line / mirReturnDoubleLine
func mirReturnI64Line(val string) string    { return mirRetLine("i64", val) }
func mirReturnPtrLine(val string) string    { return mirRetLine("ptr", val) }
func mirReturnI1Line(val string) string     { return mirRetLine("i1", val) }
func mirReturnDoubleLine(val string) string { return mirRetLine("double", val) }

// §3 panic-helper / abort-trap declares.

// Osty: mirRuntimeDeclareNoReturnVoidNoArgsLine
func mirRuntimeDeclareNoReturnVoidNoArgsLine(sym string) string {
	return mirRuntimeDeclareNoReturn("void", sym, "", false)
}

// Osty: mirRuntimeDeclareNoReturnColdVoidNoArgsLine
func mirRuntimeDeclareNoReturnColdVoidNoArgsLine(sym string) string {
	return mirRuntimeDeclareNoReturn("void", sym, "", true)
}

// §6 closure / fn-pointer call shapes.

// Osty: mirIndirectCallVoidLine / mirIndirectCallValueLine
func mirIndirectCallVoidLine(callType, fnPtrReg, argList string) string {
	return mirCallIndirectVoidLine(callType, fnPtrReg, argList)
}
func mirIndirectCallValueLine(reg, callType, fnPtrReg, argList string) string {
	return mirCallIndirectValueLine(reg, callType, fnPtrReg, argList)
}

// §6 misc store / load conveniences.

// Osty: mirStoreFromOperandLine / mirLoadIntoOperandLine
func mirStoreFromOperandLine(ty, val, slot string) string { return mirStoreLine(ty, val, slot) }
func mirLoadIntoOperandLine(reg, ty, slot string) string  { return mirLoadLine(reg, ty, slot) }

// §6 String runtime ABI specialisations (continued).

// Osty: mirCallValueStringLenLine / HashLine / IsEmptyLine
func mirCallValueStringLenLine(reg, sReg string) string {
	return mirCallValueI64FromPtrLine(reg, "osty_rt_strings_Len", sReg)
}
func mirCallValueStringHashLine(reg, sReg string) string {
	return mirCallValueI64FromPtrLine(reg, "osty_rt_strings_Hash", sReg)
}
func mirCallValueStringIsEmptyLine(reg, sReg string) string {
	return mirCallValueI1FromPtrLine(reg, "osty_rt_strings_IsEmpty", sReg)
}

// Osty: mirCallValueStringTrimLine / ToUpperLine / ToLowerLine
func mirCallValueStringTrimLine(reg, sym, sReg string) string {
	return mirCallValuePtrFromPtrLine(reg, sym, sReg)
}
func mirCallValueStringToUpperLine(reg, sReg string) string {
	return mirCallValuePtrFromPtrLine(reg, "osty_rt_strings_ToUpper", sReg)
}
func mirCallValueStringToLowerLine(reg, sReg string) string {
	return mirCallValuePtrFromPtrLine(reg, "osty_rt_strings_ToLower", sReg)
}

// Osty: mirCallValueStringStartsWith/EndsWith/ContainsLine
func mirCallValueStringStartsWithLine(reg, sReg, prefixReg string) string {
	return mirCallI1FromTwoPtrLine(reg, "osty_rt_strings_StartsWith", sReg, prefixReg)
}
func mirCallValueStringEndsWithLine(reg, sReg, suffixReg string) string {
	return mirCallI1FromTwoPtrLine(reg, "osty_rt_strings_EndsWith", sReg, suffixReg)
}
func mirCallValueStringContainsLine(reg, sReg, needleReg string) string {
	return mirCallI1FromTwoPtrLine(reg, "osty_rt_strings_Contains", sReg, needleReg)
}

// Osty: mirCallValueStringIndexOf/LastIndexOfLine
func mirCallValueStringIndexOfLine(reg, sReg, needleReg string) string {
	return mirCallI64FromTwoPtrLine(reg, "osty_rt_strings_IndexOf", sReg, needleReg)
}
func mirCallValueStringLastIndexOfLine(reg, sReg, needleReg string) string {
	return mirCallI64FromTwoPtrLine(reg, "osty_rt_strings_LastIndexOf", sReg, needleReg)
}

// Osty: mirCallValueStringSplit/Join/Replace/Repeat/DiffLinesLine
func mirCallValueStringSplitLine(reg, sReg, sepReg string) string {
	return mirCallPtrFromTwoPtrLine(reg, "osty_rt_strings_Split", sReg, sepReg)
}
func mirCallValueStringJoinLine(reg, listReg, sepReg string) string {
	return mirCallPtrFromTwoPtrLine(reg, "osty_rt_strings_Join", listReg, sepReg)
}
func mirCallValueStringReplaceLine(reg, sReg, oldReg, newReg string) string {
	return mirCallValueLine(reg, "ptr", "osty_rt_strings_Replace", "ptr "+sReg+", ptr "+oldReg+", ptr "+newReg)
}
func mirCallValueStringRepeatLine(reg, sReg, nReg string) string {
	return mirCallValueLine(reg, "ptr", "osty_rt_strings_Repeat", "ptr "+sReg+", i64 "+nReg)
}
func mirCallValueStringDiffLinesLine(reg, expectedReg, actualReg string) string {
	return mirCallPtrFromTwoPtrLine(reg, "osty_rt_strings_DiffLines", expectedReg, actualReg)
}

// §6 Bytes runtime ABI specialisations.

// Osty: mirCallValueBytesLen/Get/IndexOf/LastIndexOf/Contains/StartsWith/EndsWith/SubstringLine
func mirCallValueBytesLenLine(reg, bReg string) string {
	return mirCallValueI64FromPtrLine(reg, "osty_rt_bytes_len", bReg)
}
func mirCallValueBytesGetLine(reg, bReg, idxReg string) string {
	return mirCallValueI8FromPtrI64Line(reg, "osty_rt_bytes_get", bReg, idxReg)
}
func mirCallValueBytesIndexOfLine(reg, bReg, needleReg string) string {
	return mirCallI64FromTwoPtrLine(reg, "osty_rt_bytes_index_of", bReg, needleReg)
}
func mirCallValueBytesLastIndexOfLine(reg, bReg, needleReg string) string {
	return mirCallI64FromTwoPtrLine(reg, "osty_rt_bytes_last_index_of", bReg, needleReg)
}
func mirCallValueBytesContainsLine(reg, bReg, needleReg string) string {
	return mirCallI1FromTwoPtrLine(reg, "osty_rt_bytes_contains", bReg, needleReg)
}
func mirCallValueBytesStartsWithLine(reg, bReg, prefixReg string) string {
	return mirCallI1FromTwoPtrLine(reg, "osty_rt_bytes_starts_with", bReg, prefixReg)
}
func mirCallValueBytesEndsWithLine(reg, bReg, suffixReg string) string {
	return mirCallI1FromTwoPtrLine(reg, "osty_rt_bytes_ends_with", bReg, suffixReg)
}
func mirCallValueBytesSubstringLine(reg, bReg, startIdx, endIdx string) string {
	return mirCallValueLine(reg, "ptr", "osty_rt_bytes_substring", "ptr "+bReg+", i64 "+startIdx+", i64 "+endIdx)
}

// §6 List-runtime ABI specialisations (additional).

// Osty: mirCallValueListSliceLine / Map/Filter/FoldLine
func mirCallValueListSliceLine(reg, listReg, startIdx, endIdx string) string {
	return mirCallValueLine(reg, "ptr", "osty_rt_list_slice", "ptr "+listReg+", i64 "+startIdx+", i64 "+endIdx)
}
func mirCallValueListMapLine(reg, listReg, envReg string) string {
	return mirCallPtrFromTwoPtrLine(reg, "osty_rt_list_map", listReg, envReg)
}
func mirCallValueListFilterLine(reg, listReg, envReg string) string {
	return mirCallPtrFromTwoPtrLine(reg, "osty_rt_list_filter", listReg, envReg)
}
func mirCallValueListFoldLine(reg, listReg, envReg, seedReg string) string {
	return mirCallValueLine(reg, "ptr", "osty_rt_list_fold", "ptr "+listReg+", ptr "+envReg+", ptr "+seedReg)
}

// §6 Map runtime ABI specialisations (additional).

// Osty: mirCallValueMapValuesLine / mirCallValueMapEntriesLine / mirCallVoidMapMergeWithLine
func mirCallValueMapValuesLine(reg, mapReg string) string {
	return mirCallValuePtrFromPtrLine(reg, "osty_rt_map_values", mapReg)
}
func mirCallValueMapEntriesLine(reg, mapReg string) string {
	return mirCallValuePtrFromPtrLine(reg, "osty_rt_map_entries", mapReg)
}
func mirCallVoidMapMergeWithLine(destReg, srcReg, envReg string) string {
	return mirCallVoidFromThreePtrLine("osty_rt_map_merge_with", destReg, srcReg, envReg)
}

// §6 Set runtime ABI specialisations.

// Osty: mirCallValueSetContainsLine / mirCallVoidSetAdd/RemoveLine / mirCallValueSetToListLine
func mirCallValueSetContainsLine(reg, sym, setReg, elemLLVM, elemReg string) string {
	return mirCallI1SetElemLine(reg, sym, setReg, elemLLVM, elemReg)
}
func mirCallVoidSetAddLine(sym, setReg, elemLLVM, elemReg string) string {
	return mirCallVoidSetElemLine(sym, setReg, elemLLVM, elemReg)
}
func mirCallVoidSetRemoveLine(sym, setReg, elemLLVM, elemReg string) string {
	return mirCallVoidSetElemLine(sym, setReg, elemLLVM, elemReg)
}
func mirCallValueSetToListLine(reg, setReg string) string {
	return mirCallValuePtrFromPtrLine(reg, "osty_rt_set_to_list", setReg)
}

// §6 vector-list snapshot / data / len builders.

// Osty: mirCallValueListDataNoAliasLine
func mirCallValueListDataNoAliasLine(reg, sym, listReg, scopeRef string) string {
	return mirCallValueWithAliasScopeLine(reg, "ptr", sym, "ptr "+listReg, scopeRef)
}

// Osty: mirCallValueListLenWithScopeLine
func mirCallValueListLenWithScopeLine(reg, listReg, scopeRef string) string {
	return mirCallValueWithAliasScopeLine(reg, "i64", "osty_rt_list_len", "ptr "+listReg, scopeRef)
}

// §6 lib-call style builders — drain remaining distinct shapes.

// Osty: mirCallValueStringConcatChainLine
func mirCallValueStringConcatChainLine(reg, prevReg, nextReg string) string {
	return mirCallStringConcatLine(reg, prevReg, nextReg)
}

// Osty: mirCallValueListGetSliceLine
func mirCallValueListGetSliceLine(reg, listReg, startIdx, endIdx string) string {
	return mirCallValueListSliceLine(reg, listReg, startIdx, endIdx)
}

// §6 LLVM-text formatter helpers — drain inline `fmt.Sprintf("...%d...", n)` patterns.

// Osty: mirIntLiteralI64 / I32 / I8 / I1
func mirIntLiteralI64(digits string) string { return "i64 " + digits }
func mirIntLiteralI32(digits string) string { return "i32 " + digits }
func mirIntLiteralI8(digits string) string  { return "i8 " + digits }
func mirIntLiteralI1(digits string) string  { return "i1 " + digits }

// Osty: mirPtrLiteralLine / mirPtrNullLiteral
func mirPtrLiteralLine(symbol string) string { return "ptr " + symbol }
func mirPtrNullLiteral() string              { return "ptr null" }

// Osty: mirDoubleLiteralLine
func mirDoubleLiteralLine(digits string) string { return "double " + digits }

// Osty: mirCallVarargPrintfFourArgLine / FiveArg / SixArg
func mirCallVarargPrintfFourArgLine(fmtSym, a1, a2, a3, a4 string) string {
	return mirCallVarargPrintfLine(fmtSym, a1+", "+a2+", "+a3+", "+a4)
}
func mirCallVarargPrintfFiveArgLine(fmtSym, a1, a2, a3, a4, a5 string) string {
	return mirCallVarargPrintfLine(fmtSym, a1+", "+a2+", "+a3+", "+a4+", "+a5)
}
func mirCallVarargPrintfSixArgLine(fmtSym, a1, a2, a3, a4, a5, a6 string) string {
	return mirCallVarargPrintfLine(fmtSym, a1+", "+a2+", "+a3+", "+a4+", "+a5+", "+a6)
}

// §6 testing-helpers build shape.

// Osty: mirCallVoidTestingAbortLine
func mirCallVoidTestingAbortLine(messagePtr string) string {
	return mirCallVoidPtrLine("osty_rt_test_abort", messagePtr)
}

// Osty: mirCallVoidTestingContextEnterLine
func mirCallVoidTestingContextEnterLine(nameReg string) string {
	return mirCallVoidPtrLine("osty_rt_test_context_enter", nameReg)
}

// Osty: mirCallVoidTestingContextExitLine
func mirCallVoidTestingContextExitLine() string {
	return mirCallVoidNoArgsLine("osty_rt_test_context_exit")
}

// Osty: mirCallValueTestingExpectOkLine / mirCallValueTestingExpectErrorLine
func mirCallValueTestingExpectOkLine(reg, resultReg string) string {
	return mirCallValueI1FromPtrLine(reg, "osty_rt_test_expect_ok", resultReg)
}
func mirCallValueTestingExpectErrorLine(reg, resultReg string) string {
	return mirCallValueI1FromPtrLine(reg, "osty_rt_test_expect_error", resultReg)
}

// §6 GC root array setup builders.

// Osty: mirGCRootSlotsAllocaLine
func mirGCRootSlotsAllocaLine(slotsPtr, countDigits string) string {
	return mirAllocaArrayLine(slotsPtr, "ptr", countDigits)
}

// Osty: mirGCRootSlotStoreLine
func mirGCRootSlotStoreLine(slotPtr, slotsPtr, idxDigits, addr string) string {
	return mirGEPLine(slotPtr, "ptr", slotsPtr, "i64", idxDigits) +
		mirStorePtrLine(addr, slotPtr)
}

// §6 error-recovery / poison-fallback builders.

// Osty: mirCommentNoteLine / mirCommentTodoLine
func mirCommentNoteLine(text string) string { return "  ; NOTE: " + text + "\n" }
func mirCommentTodoLine(text string) string { return "  ; TODO: " + text + "\n" }

// §6 misc value-conversion helpers.

// Osty: mirZeroOfType
func mirZeroOfType(llvmTy string) string {
	switch llvmTy {
	case "i64", "i32", "i16", "i8":
		return "0"
	case "i1":
		return "false"
	case "double", "float":
		return "0.0"
	case "ptr":
		return "null"
	default:
		return "zeroinitializer"
	}
}

// Osty: mirOneOfType
func mirOneOfType(llvmTy string) string {
	switch llvmTy {
	case "i64", "i32", "i16", "i8":
		return "1"
	case "i1":
		return "true"
	case "double", "float":
		return "1.0"
	default:
		return "1"
	}
}

// §6 misc indent / sub-block builders.

// Osty: mirIndentedLine / mirRawLine
func mirIndentedLine(body string) string { return "  " + body + "\n" }
func mirRawLine(line string) string      { return line }

// §6 fn-attribute / fn-decl helpers.

// Osty: mirFnAttrInlineHint / AlwaysInline / NoInline / Hot / Cold / Pure
//
//	NoUnwind / WillReturn / MemoryRead / MemoryWrite / NoReturn
func mirFnAttrInlineHint() string   { return "inlinehint" }
func mirFnAttrAlwaysInline() string { return "alwaysinline" }
func mirFnAttrNoInline() string     { return "noinline" }
func mirFnAttrHot() string          { return "hot" }
func mirFnAttrCold() string         { return "cold" }
func mirFnAttrPure() string         { return "readnone" }
func mirFnAttrNoUnwind() string     { return "nounwind" }
func mirFnAttrWillReturn() string   { return "willreturn" }
func mirFnAttrMemoryRead() string   { return "memory(read)" }
func mirFnAttrMemoryWrite() string  { return "memory(write)" }
func mirFnAttrNoReturn() string     { return "noreturn" }

// §6 LLVM linkage / visibility builders.

// Osty: mirLinkageInternal / Private / External
func mirLinkageInternal() string { return "internal" }
func mirLinkagePrivate() string  { return "private" }
func mirLinkageExternal() string { return "external" }

// §6 LLVM-text constants.

// Osty: mirUnnamedAddrTag / mirConstantTag / mirGlobalTag
func mirUnnamedAddrTag() string { return "unnamed_addr" }
func mirConstantTag() string    { return "constant" }
func mirGlobalTag() string      { return "global" }

// §6 metadata / debug-info builders.

// Osty: mirNullMDRef / mirZeroinitializerLiteral / mirUndefLiteral / mirPoisonLiteral
func mirNullMDRef() string              { return "null" }
func mirZeroinitializerLiteral() string { return "zeroinitializer" }
func mirUndefLiteral() string           { return "undef" }
func mirPoisonLiteral() string          { return "poison" }

// §6 callee-shape rendering helpers.

// Osty: mirCalleeFnRefText / mirCalleeIndirectText
func mirCalleeFnRefText(symbol string) string { return "@" + symbol }
func mirCalleeIndirectText(reg string) string { return reg }

// §6 misc text-shape helpers.

// Osty: mirEqualsAssign / mirAttachComma / mirAttachSpace / mirNewline
func mirEqualsAssign() string { return " = " }
func mirAttachComma() string  { return ", " }
func mirAttachSpace() string  { return " " }
func mirNewline() string      { return "\n" }

// §6 LLVM linkage / linkage-attr composite tokens.

// Osty: mirPrivateUnnamedAddrConstantTag / mirInternalUnnamedAddrConstantTag / mirInternalGlobalTag
func mirPrivateUnnamedAddrConstantTag() string {
	return mirLinkagePrivate() + " " + mirUnnamedAddrTag() + " " + mirConstantTag()
}
func mirInternalUnnamedAddrConstantTag() string {
	return mirLinkageInternal() + " " + mirUnnamedAddrTag() + " " + mirConstantTag()
}
func mirInternalGlobalTag() string {
	return mirLinkageInternal() + " " + mirGlobalTag()
}

// §6 typed-aggregate literal builders.

// Osty: mirAggregateUndef / mirAggregatePoison / mirAggregateZero
func mirAggregateUndef() string  { return mirUndefLiteral() }
func mirAggregatePoison() string { return mirPoisonLiteral() }
func mirAggregateZero() string   { return mirZeroinitializerLiteral() }

// §6 SSA-register / sigil helpers.

// Osty: mirLocalReg / mirGlobalSym / mirMetadataRef
func mirLocalReg(name string) string    { return "%" + name }
func mirGlobalSym(name string) string   { return "@" + name }
func mirMetadataRef(name string) string { return "!" + name }

// §6 typed-arg slot composers.

// Osty: mirArgSlotPtr / I64 / I32 / I1 / I8 / Double
func mirArgSlotPtr(reg string) string    { return "ptr " + reg }
func mirArgSlotI64(reg string) string    { return "i64 " + reg }
func mirArgSlotI32(reg string) string    { return "i32 " + reg }
func mirArgSlotI1(reg string) string     { return "i1 " + reg }
func mirArgSlotI8(reg string) string     { return "i8 " + reg }
func mirArgSlotDouble(reg string) string { return "double " + reg }

// §6 typed two-arg shapes.

// Osty: mirArgListTwoPtr / mirArgListPtrI64 / mirArgListPtrI64I64 / mirArgListThreePtr
func mirArgListTwoPtr(a, b string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotPtr(b)
}
func mirArgListPtrI64(a, b string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotI64(b)
}
func mirArgListPtrI64I64(a, b, c string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotI64(b) + ", " + mirArgSlotI64(c)
}
func mirArgListThreePtr(a, b, c string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotPtr(b) + ", " + mirArgSlotPtr(c)
}

// §6 LLVM-text type-token helpers.

// Osty: mirTypeI64 / I32 / I16 / I8 / I1 / mirTypePtr / mirTypeDouble / mirTypeFloat / mirTypeVoid
func mirTypeI64() string    { return "i64" }
func mirTypeI32() string    { return "i32" }
func mirTypeI16() string    { return "i16" }
func mirTypeI8() string     { return "i8" }
func mirTypeI1() string     { return "i1" }
func mirTypePtr() string    { return "ptr" }
func mirTypeDouble() string { return "double" }
func mirTypeFloat() string  { return "float" }
func mirTypeVoid() string   { return "void" }

// §6 LLVM call-attribute tokens.

// Osty: mirCallAttrTail / MustTail / NoTail
func mirCallAttrTail() string     { return "tail" }
func mirCallAttrMustTail() string { return "musttail" }
func mirCallAttrNoTail() string   { return "notail" }

// §6 LLVM ParamAttr tokens.

// Osty: mirParamAttrNoAlias / NoCapture / ReadOnly / WriteOnly
func mirParamAttrNoAlias() string   { return "noalias" }
func mirParamAttrNoCapture() string { return "nocapture" }
func mirParamAttrReadOnly() string  { return "readonly" }
func mirParamAttrWriteOnly() string { return "writeonly" }

// §6 LLVM cmp-predicate tokens.

// Osty: mirICmpEq / Ne / Slt / Sle / Sgt / Sge / Ult / Ule / Ugt / Uge
func mirICmpEq() string  { return "eq" }
func mirICmpNe() string  { return "ne" }
func mirICmpSlt() string { return "slt" }
func mirICmpSle() string { return "sle" }
func mirICmpSgt() string { return "sgt" }
func mirICmpSge() string { return "sge" }
func mirICmpUlt() string { return "ult" }
func mirICmpUle() string { return "ule" }
func mirICmpUgt() string { return "ugt" }
func mirICmpUge() string { return "uge" }

// Osty: mirFCmpOEq / One / Olt / Ole / Ogt / Oge
func mirFCmpOEq() string { return "oeq" }
func mirFCmpOne() string { return "one" }
func mirFCmpOlt() string { return "olt" }
func mirFCmpOle() string { return "ole" }
func mirFCmpOgt() string { return "ogt" }
func mirFCmpOge() string { return "oge" }

// §6 binary-op token names.

// Osty: mirOpAdd / Sub / Mul / SDiv / SRem / UDiv / URem
func mirOpAdd() string  { return "add" }
func mirOpSub() string  { return "sub" }
func mirOpMul() string  { return "mul" }
func mirOpSDiv() string { return "sdiv" }
func mirOpSRem() string { return "srem" }
func mirOpUDiv() string { return "udiv" }
func mirOpURem() string { return "urem" }

// Osty: mirOpFAdd / FSub / FMul / FDiv / FRem
func mirOpFAdd() string { return "fadd" }
func mirOpFSub() string { return "fsub" }
func mirOpFMul() string { return "fmul" }
func mirOpFDiv() string { return "fdiv" }
func mirOpFRem() string { return "frem" }

// Osty: mirOpAnd / Or / Xor
func mirOpAnd() string { return "and" }
func mirOpOr() string  { return "or" }
func mirOpXor() string { return "xor" }

// Osty: mirOpShl / LShr / AShr
func mirOpShl() string  { return "shl" }
func mirOpLShr() string { return "lshr" }
func mirOpAShr() string { return "ashr" }

// §6 cast-op token names.

// Osty: mirCastSExt / ZExt / Trunc
func mirCastSExt() string  { return "sext" }
func mirCastZExt() string  { return "zext" }
func mirCastTrunc() string { return "trunc" }

// Osty: mirCastSIToFP / UIToFP / FPToSI / FPToUI
func mirCastSIToFP() string { return "sitofp" }
func mirCastUIToFP() string { return "uitofp" }
func mirCastFPToSI() string { return "fptosi" }
func mirCastFPToUI() string { return "fptoui" }

// Osty: mirCastFPExt / FPTrunc / Bitcast / PtrToInt / IntToPtr / AddrSpace
func mirCastFPExt() string     { return "fpext" }
func mirCastFPTrunc() string   { return "fptrunc" }
func mirCastBitcast() string   { return "bitcast" }
func mirCastPtrToInt() string  { return "ptrtoint" }
func mirCastIntToPtr() string  { return "inttoptr" }
func mirCastAddrSpace() string { return "addrspacecast" }

// §6 terminator-name token helpers.

// Osty: mirTermBr / Switch / Ret / Unreachable / Invoke / Resume
func mirTermBr() string          { return "br" }
func mirTermSwitch() string      { return "switch" }
func mirTermRet() string         { return "ret" }
func mirTermUnreachable() string { return "unreachable" }
func mirTermInvoke() string      { return "invoke" }
func mirTermResume() string      { return "resume" }

// §6 instruction-name token helpers.

// Osty: mirInstrAlloca / Load / Store / GEP / GEPInBounds / Call / CallVoid
func mirInstrAlloca() string      { return "alloca" }
func mirInstrLoad() string        { return "load" }
func mirInstrStore() string       { return "store" }
func mirInstrGEP() string         { return "getelementptr" }
func mirInstrGEPInBounds() string { return "getelementptr inbounds" }
func mirInstrCall() string        { return "call" }
func mirInstrCallVoid() string    { return "call" }

// Osty: mirInstrPhi / Select / InsertValue / ExtractValue / ICmp / FCmp
func mirInstrPhi() string          { return "phi" }
func mirInstrSelect() string       { return "select" }
func mirInstrInsertValue() string  { return "insertvalue" }
func mirInstrExtractValue() string { return "extractvalue" }
func mirInstrICmp() string         { return "icmp" }
func mirInstrFCmp() string         { return "fcmp" }

// Osty: mirInstrAtomicRMW / CmpXchg / Fence
func mirInstrAtomicRMW() string { return "atomicrmw" }
func mirInstrCmpXchg() string   { return "cmpxchg" }
func mirInstrFence() string     { return "fence" }

// §6 atomic ordering tokens.

// Osty: mirAtomicUnordered / Monotonic / Acquire / Release / AcqRel / SeqCst
func mirAtomicUnordered() string { return "unordered" }
func mirAtomicMonotonic() string { return "monotonic" }
func mirAtomicAcquire() string   { return "acquire" }
func mirAtomicRelease() string   { return "release" }
func mirAtomicAcqRel() string    { return "acq_rel" }
func mirAtomicSeqCst() string    { return "seq_cst" }

// §6 typed-return shape helpers.

// Osty: mirRetTypedLine — generic typed-value-return composer.
// (mirRetVoidLine / mirUnreachableLine continue to live at their
// original sites earlier in this file.)
func mirRetTypedLine(ty, val string) string {
	return "  " + mirTermRet() + " " + ty + " " + val + "\n"
}

// §6 unconditional-branch shape helper.

// Osty: mirBrLabelLine — generic unconditional branch.
// (mirBrCondLine continues to live at its original site.)
func mirBrLabelLine(dst string) string {
	return "  " + mirTermBr() + " label %" + dst + "\n"
}

// §6 switch-shape helpers.

// Osty: mirSwitchHeaderLine / mirSwitchCaseLine / mirSwitchFooterLine
func mirSwitchHeaderLine(ty, val, defaultLbl string) string {
	return "  " + mirTermSwitch() + " " + ty + " " + val + ", label %" + defaultLbl + " [\n"
}
func mirSwitchCaseLine(ty, val, caseLbl string) string {
	return "    " + ty + " " + val + ", label %" + caseLbl + "\n"
}
func mirSwitchFooterLine() string { return "  ]\n" }

// §6 LLVM math-intrinsic name helpers.

// Osty: mirIntrinsicLLVMSqrtF64 / SqrtF32 / FAbsF64 / FAbsF32 / FMAF64 / FMAF32
func mirIntrinsicLLVMSqrtF64() string { return "llvm.sqrt.f64" }
func mirIntrinsicLLVMSqrtF32() string { return "llvm.sqrt.f32" }
func mirIntrinsicLLVMFAbsF64() string { return "llvm.fabs.f64" }
func mirIntrinsicLLVMFAbsF32() string { return "llvm.fabs.f32" }
func mirIntrinsicLLVMFMAF64() string  { return "llvm.fma.f64" }
func mirIntrinsicLLVMFMAF32() string  { return "llvm.fma.f32" }

// Osty: SinF64 / CosF64 / TanF64 / LogF64 / Log2F64 / Log10F64 / ExpF64 / Exp2F64 / PowF64 / PowI64
func mirIntrinsicLLVMSinF64() string   { return "llvm.sin.f64" }
func mirIntrinsicLLVMCosF64() string   { return "llvm.cos.f64" }
func mirIntrinsicLLVMTanF64() string   { return "llvm.tan.f64" }
func mirIntrinsicLLVMLogF64() string   { return "llvm.log.f64" }
func mirIntrinsicLLVMLog2F64() string  { return "llvm.log2.f64" }
func mirIntrinsicLLVMLog10F64() string { return "llvm.log10.f64" }
func mirIntrinsicLLVMExpF64() string   { return "llvm.exp.f64" }
func mirIntrinsicLLVMExp2F64() string  { return "llvm.exp2.f64" }
func mirIntrinsicLLVMPowF64() string   { return "llvm.pow.f64" }
func mirIntrinsicLLVMPowI64() string   { return "llvm.powi.f64.i32" }

// Osty: MinNumF64 / MaxNumF64
func mirIntrinsicLLVMMinNumF64() string { return "llvm.minnum.f64" }
func mirIntrinsicLLVMMaxNumF64() string { return "llvm.maxnum.f64" }

// §6 LLVM bit-manipulation intrinsic names.

// Osty: CtlzI64 / CttzI64 / CtpopI64 / BSwap{I64,I32,I16} / BitReverseI64
func mirIntrinsicLLVMCtlzI64() string       { return "llvm.ctlz.i64" }
func mirIntrinsicLLVMCttzI64() string       { return "llvm.cttz.i64" }
func mirIntrinsicLLVMCtpopI64() string      { return "llvm.ctpop.i64" }
func mirIntrinsicLLVMBSwapI64() string      { return "llvm.bswap.i64" }
func mirIntrinsicLLVMBSwapI32() string      { return "llvm.bswap.i32" }
func mirIntrinsicLLVMBSwapI16() string      { return "llvm.bswap.i16" }
func mirIntrinsicLLVMBitReverseI64() string { return "llvm.bitreverse.i64" }

// §6 LLVM checked-arithmetic intrinsic names.

// Osty: SAddOverflowI64 / SSubOverflowI64 / SMulOverflowI64 / U-variants
func mirIntrinsicLLVMSAddOverflowI64() string { return "llvm.sadd.with.overflow.i64" }
func mirIntrinsicLLVMSSubOverflowI64() string { return "llvm.ssub.with.overflow.i64" }
func mirIntrinsicLLVMSMulOverflowI64() string { return "llvm.smul.with.overflow.i64" }
func mirIntrinsicLLVMUAddOverflowI64() string { return "llvm.uadd.with.overflow.i64" }
func mirIntrinsicLLVMUSubOverflowI64() string { return "llvm.usub.with.overflow.i64" }
func mirIntrinsicLLVMUMulOverflowI64() string { return "llvm.umul.with.overflow.i64" }

// §6 LLVM saturating-arithmetic intrinsic names.

// Osty: SAddSatI64 / SSubSatI64 / SShlSatI64 / U-variants
func mirIntrinsicLLVMSAddSatI64() string { return "llvm.sadd.sat.i64" }
func mirIntrinsicLLVMSSubSatI64() string { return "llvm.ssub.sat.i64" }
func mirIntrinsicLLVMSShlSatI64() string { return "llvm.sshl.sat.i64" }
func mirIntrinsicLLVMUAddSatI64() string { return "llvm.uadd.sat.i64" }
func mirIntrinsicLLVMUSubSatI64() string { return "llvm.usub.sat.i64" }
func mirIntrinsicLLVMUShlSatI64() string { return "llvm.ushl.sat.i64" }

// §6 LLVM memory / lifecycle intrinsic names.

// Osty: Memcpy / Memmove / Memset / LifetimeStart / LifetimeEnd / InvariantStart / InvariantEnd / Assume / ExpectI1
func mirIntrinsicLLVMMemcpy() string         { return "llvm.memcpy.p0.p0.i64" }
func mirIntrinsicLLVMMemmove() string        { return "llvm.memmove.p0.p0.i64" }
func mirIntrinsicLLVMMemset() string         { return "llvm.memset.p0.i64" }
func mirIntrinsicLLVMLifetimeStart() string  { return "llvm.lifetime.start.p0" }
func mirIntrinsicLLVMLifetimeEnd() string    { return "llvm.lifetime.end.p0" }
func mirIntrinsicLLVMInvariantStart() string { return "llvm.invariant.start.p0" }
func mirIntrinsicLLVMInvariantEnd() string   { return "llvm.invariant.end.p0" }
func mirIntrinsicLLVMAssume() string         { return "llvm.assume" }
func mirIntrinsicLLVMExpectI1() string       { return "llvm.expect.i1" }

// Osty: StackSave / StackRestore / DbgDeclare / DbgValue
func mirIntrinsicLLVMStackSave() string    { return "llvm.stacksave" }
func mirIntrinsicLLVMStackRestore() string { return "llvm.stackrestore" }
func mirIntrinsicLLVMDbgDeclare() string   { return "llvm.dbg.declare" }
func mirIntrinsicLLVMDbgValue() string     { return "llvm.dbg.value" }

// §6 LLVM-text instruction shape helpers.

// Osty: mirAddIntLine / SubIntLine / MulIntLine / SDivIntLine / SRemIntLine
func mirAddIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpAdd() + " " + ty + " " + a + ", " + b + "\n"
}
func mirSubIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpSub() + " " + ty + " " + a + ", " + b + "\n"
}
func mirMulIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpMul() + " " + ty + " " + a + ", " + b + "\n"
}
func mirSDivIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpSDiv() + " " + ty + " " + a + ", " + b + "\n"
}
func mirSRemIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpSRem() + " " + ty + " " + a + ", " + b + "\n"
}

// Osty: mirAndIntLine / OrIntLine / XorIntLine
func mirAndIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpAnd() + " " + ty + " " + a + ", " + b + "\n"
}
func mirOrIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpOr() + " " + ty + " " + a + ", " + b + "\n"
}
func mirXorIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpXor() + " " + ty + " " + a + ", " + b + "\n"
}

// Osty: mirShlIntLine / LShrIntLine / AShrIntLine
func mirShlIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpShl() + " " + ty + " " + a + ", " + b + "\n"
}
func mirLShrIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpLShr() + " " + ty + " " + a + ", " + b + "\n"
}
func mirAShrIntLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpAShr() + " " + ty + " " + a + ", " + b + "\n"
}

// Osty: mirFRemLine — typed fp-rem shape (mirFAddLine / FSubLine /
// FMulLine / FDivLine continue to live at their original sites
// earlier in this file).
func mirFRemLine(reg, ty, a, b string) string {
	return "  " + reg + " = " + mirOpFRem() + " " + ty + " " + a + ", " + b + "\n"
}

// §6 cast-instruction shape helpers.

// Osty: mirCastLine — generic cast-instruction line composer
func mirCastLine(reg, op, fromTy, val, toTy string) string {
	return "  " + reg + " = " + op + " " + fromTy + " " + val + " to " + toTy + "\n"
}

// Osty: mirUIToFPLine / mirFPToUILine / mirFPExtLine / mirFPTruncLine
// (mirSIToFPLine / mirFPToSILine / mirBitcastLine / mirPtrToIntLine /
// mirIntToPtrLine / mirSExtLine / mirZExtLine / mirTruncLine continue
// to live at their original sites earlier in this file.)
func mirUIToFPLine(reg, fromTy, val, toTy string) string {
	return mirCastLine(reg, mirCastUIToFP(), fromTy, val, toTy)
}
func mirFPToUILine(reg, fromTy, val, toTy string) string {
	return mirCastLine(reg, mirCastFPToUI(), fromTy, val, toTy)
}
func mirFPExtLine(reg, fromTy, val, toTy string) string {
	return mirCastLine(reg, mirCastFPExt(), fromTy, val, toTy)
}
func mirFPTruncLine(reg, fromTy, val, toTy string) string {
	return mirCastLine(reg, mirCastFPTrunc(), fromTy, val, toTy)
}

// §6 specialised cast shapes — common width transitions.

// Osty: mirSExtI32ToI64Line / I16ToI64Line / I8ToI64Line / I1ToI64Line
func mirSExtI32ToI64Line(reg, val string) string {
	return mirSExtLine(reg, mirTypeI32(), val, mirTypeI64())
}
func mirSExtI16ToI64Line(reg, val string) string {
	return mirSExtLine(reg, mirTypeI16(), val, mirTypeI64())
}
func mirSExtI8ToI64Line(reg, val string) string {
	return mirSExtLine(reg, mirTypeI8(), val, mirTypeI64())
}
func mirSExtI1ToI64Line(reg, val string) string {
	return mirSExtLine(reg, mirTypeI1(), val, mirTypeI64())
}

// Osty: mirZExtI32ToI64Line / I16ToI64Line / I8ToI64Line / I1ToI64Line
func mirZExtI32ToI64Line(reg, val string) string {
	return mirZExtLine(reg, mirTypeI32(), val, mirTypeI64())
}
func mirZExtI16ToI64Line(reg, val string) string {
	return mirZExtLine(reg, mirTypeI16(), val, mirTypeI64())
}
func mirZExtI8ToI64Line(reg, val string) string {
	return mirZExtLine(reg, mirTypeI8(), val, mirTypeI64())
}
func mirZExtI1ToI64Line(reg, val string) string {
	return mirZExtLine(reg, mirTypeI1(), val, mirTypeI64())
}

// Osty: mirTruncI64ToI32Line / I16Line / I8Line / I1Line
func mirTruncI64ToI32Line(reg, val string) string {
	return mirTruncLine(reg, mirTypeI64(), val, mirTypeI32())
}
func mirTruncI64ToI16Line(reg, val string) string {
	return mirTruncLine(reg, mirTypeI64(), val, mirTypeI16())
}
func mirTruncI64ToI8Line(reg, val string) string {
	return mirTruncLine(reg, mirTypeI64(), val, mirTypeI8())
}
func mirTruncI64ToI1Line(reg, val string) string {
	return mirTruncLine(reg, mirTypeI64(), val, mirTypeI1())
}

// Osty: mirSIToFPI64ToDoubleLine / mirFPToSIDoubleToI64Line
// (mirFPExtFloatToDoubleLine / mirFPTruncDoubleToFloatLine continue
// to live at their original sites earlier in this file.)
func mirSIToFPI64ToDoubleLine(reg, val string) string {
	return mirSIToFPLine(reg, mirTypeI64(), val, mirTypeDouble())
}
func mirFPToSIDoubleToI64Line(reg, val string) string {
	return mirFPToSILine(reg, mirTypeDouble(), val, mirTypeI64())
}

// §6 icmp / fcmp instruction shape helpers.
// (mirICmpLine / mirFCmpLine continue to live at their original sites
// earlier in this file. The new specialised siblings below call
// through to the existing generic composers.)

// Osty: mirICmpI64EqLine / NeLine / SltLine / SleLine / SgtLine / SgeLine
func mirICmpI64EqLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpEq(), mirTypeI64(), a, b)
}
func mirICmpI64NeLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpNe(), mirTypeI64(), a, b)
}
func mirICmpI64SltLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpSlt(), mirTypeI64(), a, b)
}
func mirICmpI64SleLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpSle(), mirTypeI64(), a, b)
}
func mirICmpI64SgtLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpSgt(), mirTypeI64(), a, b)
}
func mirICmpI64SgeLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpSge(), mirTypeI64(), a, b)
}

// Osty: mirICmpPtrEqLine / NeLine
func mirICmpPtrEqLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpEq(), mirTypePtr(), a, b)
}
func mirICmpPtrNeLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpNe(), mirTypePtr(), a, b)
}

// Osty: mirICmpI1EqLine / NeLine
func mirICmpI1EqLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpEq(), mirTypeI1(), a, b)
}
func mirICmpI1NeLine(reg, a, b string) string {
	return mirICmpLine(reg, mirICmpNe(), mirTypeI1(), a, b)
}

// Osty: mirFCmpDoubleOEqLine / OneLine / OltLine / OleLine / OgtLine / OgeLine
func mirFCmpDoubleOEqLine(reg, a, b string) string {
	return mirFCmpLine(reg, mirFCmpOEq(), mirTypeDouble(), a, b)
}
func mirFCmpDoubleOneLine(reg, a, b string) string {
	return mirFCmpLine(reg, mirFCmpOne(), mirTypeDouble(), a, b)
}
func mirFCmpDoubleOltLine(reg, a, b string) string {
	return mirFCmpLine(reg, mirFCmpOlt(), mirTypeDouble(), a, b)
}
func mirFCmpDoubleOleLine(reg, a, b string) string {
	return mirFCmpLine(reg, mirFCmpOle(), mirTypeDouble(), a, b)
}
func mirFCmpDoubleOgtLine(reg, a, b string) string {
	return mirFCmpLine(reg, mirFCmpOgt(), mirTypeDouble(), a, b)
}
func mirFCmpDoubleOgeLine(reg, a, b string) string {
	return mirFCmpLine(reg, mirFCmpOge(), mirTypeDouble(), a, b)
}

// §6 bitwise specialised shapes.
// (mirAndI64Line / OrI64Line / XorI64Line / ShlI64Line / LShrI64Line /
// AShrI64Line / AndI1Line continue to live at their original sites
// earlier in this file. The new sibling builders below cover the
// remaining I1 / immediate variants.)

// Osty: mirXorI1Line — sibling of existing AndI1Line / OrI1Line.
func mirXorI1Line(reg, a, b string) string {
	return mirXorIntLine(reg, mirTypeI1(), a, b)
}

// §6 specialised arith shapes.

// Osty: mirSubI64ImmediateLine / mirAddI64ImmediateLine / mirSRemI64Line
// (mirMulI64Line / mirSDivI64Line continue to live at their original
// sites earlier in this file.)
func mirSubI64ImmediateLine(reg, a, imm string) string {
	return mirSubIntLine(reg, mirTypeI64(), a, imm)
}
func mirAddI64ImmediateLine(reg, a, imm string) string {
	return mirAddIntLine(reg, mirTypeI64(), a, imm)
}
func mirSRemI64Line(reg, a, b string) string {
	return mirSRemIntLine(reg, mirTypeI64(), a, b)
}

// Osty: mirFAddDoubleLine / FSubDoubleLine / FMulDoubleLine / FDivDoubleLine / FRemDoubleLine
func mirFAddDoubleLine(reg, a, b string) string {
	return mirFAddLine(reg, mirTypeDouble(), a, b)
}
func mirFSubDoubleLine(reg, a, b string) string {
	return mirFSubLine(reg, mirTypeDouble(), a, b)
}
func mirFMulDoubleLine(reg, a, b string) string {
	return mirFMulLine(reg, mirTypeDouble(), a, b)
}
func mirFDivDoubleLine(reg, a, b string) string {
	return mirFDivLine(reg, mirTypeDouble(), a, b)
}
func mirFRemDoubleLine(reg, a, b string) string {
	return mirFRemLine(reg, mirTypeDouble(), a, b)
}

// §6 LLVM math-intrinsic typed-call shapes.

// Osty: mirCallValueDoubleFromDoubleLine — base composer
func mirCallValueDoubleFromDoubleLine(reg, sym, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @" + sym + "(double " + x + ")\n"
}

// Osty: mirCallValueLLVMSqrtF64Line / FAbsF64Line
func mirCallValueLLVMSqrtF64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMSqrtF64(), x)
}
func mirCallValueLLVMFAbsF64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMFAbsF64(), x)
}

// Osty: SinF64Line / CosF64Line / TanF64Line / LogF64Line / Log2F64Line / Log10F64Line / ExpF64Line / Exp2F64Line
func mirCallValueLLVMSinF64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMSinF64(), x)
}
func mirCallValueLLVMCosF64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMCosF64(), x)
}
func mirCallValueLLVMTanF64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMTanF64(), x)
}
func mirCallValueLLVMLogF64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMLogF64(), x)
}
func mirCallValueLLVMLog2F64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMLog2F64(), x)
}
func mirCallValueLLVMLog10F64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMLog10F64(), x)
}
func mirCallValueLLVMExpF64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMExpF64(), x)
}
func mirCallValueLLVMExp2F64Line(reg, x string) string {
	return mirCallValueDoubleFromDoubleLine(reg, mirIntrinsicLLVMExp2F64(), x)
}

// Osty: mirCallValueLLVMPowF64Line / MinNumF64Line / MaxNumF64Line
func mirCallValueLLVMPowF64Line(reg, base, exp string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @" + mirIntrinsicLLVMPowF64() + "(double " + base + ", double " + exp + ")\n"
}
func mirCallValueLLVMMinNumF64Line(reg, a, b string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @" + mirIntrinsicLLVMMinNumF64() + "(double " + a + ", double " + b + ")\n"
}
func mirCallValueLLVMMaxNumF64Line(reg, a, b string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @" + mirIntrinsicLLVMMaxNumF64() + "(double " + a + ", double " + b + ")\n"
}

// §6 LLVM bit-manipulation typed-call shapes.

// Osty: CtlzI64Line / CttzI64Line
func mirCallValueLLVMCtlzI64Line(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + mirIntrinsicLLVMCtlzI64() + "(i64 " + x + ", i1 false)\n"
}
func mirCallValueLLVMCttzI64Line(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + mirIntrinsicLLVMCttzI64() + "(i64 " + x + ", i1 false)\n"
}

// Osty: CtpopI64Line
func mirCallValueLLVMCtpopI64Line(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + mirIntrinsicLLVMCtpopI64() + "(i64 " + x + ")\n"
}

// Osty: BSwapI64Line / I32Line / I16Line
func mirCallValueLLVMBSwapI64Line(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + mirIntrinsicLLVMBSwapI64() + "(i64 " + x + ")\n"
}
func mirCallValueLLVMBSwapI32Line(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " i32 @" + mirIntrinsicLLVMBSwapI32() + "(i32 " + x + ")\n"
}
func mirCallValueLLVMBSwapI16Line(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " i16 @" + mirIntrinsicLLVMBSwapI16() + "(i16 " + x + ")\n"
}

// Osty: BitReverseI64Line
func mirCallValueLLVMBitReverseI64Line(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + mirIntrinsicLLVMBitReverseI64() + "(i64 " + x + ")\n"
}

// §6 alloca-shape helpers.

// Osty: mirAllocaSingleLine / mirAllocaSingleAlignedLine
func mirAllocaSingleLine(reg, ty string) string {
	return "  " + reg + " = " + mirInstrAlloca() + " " + ty + "\n"
}
func mirAllocaSingleAlignedLine(reg, ty, alignDigits string) string {
	return "  " + reg + " = " + mirInstrAlloca() + " " + ty + ", align " + alignDigits + "\n"
}

// §6 typed-alloca shape helpers.

// Osty: mirAllocaPtrLine / I64Line / I32Line / I8Line / I1Line / DoubleLine
func mirAllocaPtrLine(reg string) string    { return mirAllocaSingleLine(reg, mirTypePtr()) }
func mirAllocaI64Line(reg string) string    { return mirAllocaSingleLine(reg, mirTypeI64()) }
func mirAllocaI32Line(reg string) string    { return mirAllocaSingleLine(reg, mirTypeI32()) }
func mirAllocaI8Line(reg string) string     { return mirAllocaSingleLine(reg, mirTypeI8()) }
func mirAllocaI1Line(reg string) string     { return mirAllocaSingleLine(reg, mirTypeI1()) }
func mirAllocaDoubleLine(reg string) string { return mirAllocaSingleLine(reg, mirTypeDouble()) }

// §6 lifetime / invariant intrinsic call shapes.

// Osty: mirCallVoidLLVMLifetimeStartLine / EndLine / AssumeLine / mirCallValueLLVMExpectI1Line
func mirCallVoidLLVMLifetimeStartLine(sizeDigits, slot string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirIntrinsicLLVMLifetimeStart() + "(i64 " + sizeDigits + ", ptr " + slot + ")\n"
}
func mirCallVoidLLVMLifetimeEndLine(sizeDigits, slot string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirIntrinsicLLVMLifetimeEnd() + "(i64 " + sizeDigits + ", ptr " + slot + ")\n"
}
func mirCallVoidLLVMAssumeLine(cond string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirIntrinsicLLVMAssume() + "(i1 " + cond + ")\n"
}
func mirCallValueLLVMExpectI1Line(reg, cond, expected string) string {
	return "  " + reg + " = " + mirInstrCall() + " i1 @" + mirIntrinsicLLVMExpectI1() + "(i1 " + cond + ", i1 " + expected + ")\n"
}

// §6 memcpy / memmove / memset intrinsic call shapes.

// Osty: mirCallVoidLLVMMemcpyLine / MemmoveLine / MemsetLine
func mirCallVoidLLVMMemcpyLine(dst, src, sizeDigits, isVolatile string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirIntrinsicLLVMMemcpy() + "(ptr " + dst + ", ptr " + src + ", i64 " + sizeDigits + ", i1 " + isVolatile + ")\n"
}
func mirCallVoidLLVMMemmoveLine(dst, src, sizeDigits, isVolatile string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirIntrinsicLLVMMemmove() + "(ptr " + dst + ", ptr " + src + ", i64 " + sizeDigits + ", i1 " + isVolatile + ")\n"
}
func mirCallVoidLLVMMemsetLine(dst, val, sizeDigits, isVolatile string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirIntrinsicLLVMMemset() + "(ptr " + dst + ", i8 " + val + ", i64 " + sizeDigits + ", i1 " + isVolatile + ")\n"
}

// §6 more linkage tag combos.

// Osty: mirInternalConstantTag / mirPrivateConstantTag / mirExternalGlobalTag / mirExternalFnTag
func mirInternalConstantTag() string { return mirLinkageInternal() + " " + mirConstantTag() }
func mirPrivateConstantTag() string  { return mirLinkagePrivate() + " " + mirConstantTag() }
func mirExternalGlobalTag() string   { return mirLinkageExternal() + " " + mirGlobalTag() }
func mirExternalFnTag() string       { return mirLinkageExternal() }

// §6 string-pool / global-data shape helpers.

// Osty: mirGlobalStringPoolDeclLine / mirGlobalConstantI64DeclLine / mirGlobalConstantPtrDeclLine
//
//	/ mirGlobalMutableI64DeclLine / mirGlobalMutablePtrDeclLine
func mirGlobalStringPoolDeclLine(sym, sizeDigits, encoded string) string {
	return sym + " = " + mirPrivateUnnamedAddrConstantTag() + " [" + sizeDigits + " x i8] c\"" + encoded + "\"\n"
}
func mirGlobalConstantI64DeclLine(sym, val string) string {
	return sym + " = " + mirInternalConstantTag() + " i64 " + val + "\n"
}
func mirGlobalConstantPtrDeclLine(sym, val string) string {
	return sym + " = " + mirInternalConstantTag() + " ptr " + val + "\n"
}
func mirGlobalMutableI64DeclLine(sym, init string) string {
	return sym + " = " + mirInternalGlobalTag() + " i64 " + init + "\n"
}
func mirGlobalMutablePtrDeclLine(sym, init string) string {
	return sym + " = " + mirInternalGlobalTag() + " ptr " + init + "\n"
}

// §6 store / load specialisation helpers.

// Osty: mirStoreI8Line / I32Line / DoubleLine / FloatLine
func mirStoreI8Line(val, slot string) string {
	return "  " + mirInstrStore() + " i8 " + val + ", ptr " + slot + "\n"
}
func mirStoreI32Line(val, slot string) string {
	return "  " + mirInstrStore() + " i32 " + val + ", ptr " + slot + "\n"
}
func mirStoreDoubleLine(val, slot string) string {
	return "  " + mirInstrStore() + " double " + val + ", ptr " + slot + "\n"
}
func mirStoreFloatLine(val, slot string) string {
	return "  " + mirInstrStore() + " float " + val + ", ptr " + slot + "\n"
}

// Osty: mirLoadI8Line / I32Line / FloatLine
func mirLoadI8Line(reg, slot string) string {
	return "  " + reg + " = " + mirInstrLoad() + " i8, ptr " + slot + "\n"
}
func mirLoadI32Line(reg, slot string) string {
	return "  " + reg + " = " + mirInstrLoad() + " i32, ptr " + slot + "\n"
}
func mirLoadFloatLine(reg, slot string) string {
	return "  " + reg + " = " + mirInstrLoad() + " float, ptr " + slot + "\n"
}

// §6 GEP-shape specialisation helpers.

// Osty: mirGEPI8StrideLine / I32StrideLine / I16StrideLine / FloatStrideLine
func mirGEPI8StrideLine(reg, basePtr, idx string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " i8, ptr " + basePtr + ", i64 " + idx + "\n"
}
func mirGEPI32StrideLine(reg, basePtr, idx string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " i32, ptr " + basePtr + ", i64 " + idx + "\n"
}
func mirGEPI16StrideLine(reg, basePtr, idx string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " i16, ptr " + basePtr + ", i64 " + idx + "\n"
}
func mirGEPFloatStrideLine(reg, basePtr, idx string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " float, ptr " + basePtr + ", i64 " + idx + "\n"
}

// §6 alloca-array shape specialisations.

// Osty: mirAllocaArrayPtrLine / I64Line / I8Line
func mirAllocaArrayPtrLine(reg, countDigits string) string {
	return mirAllocaArrayLine(reg, mirTypePtr(), countDigits)
}
func mirAllocaArrayI64Line(reg, countDigits string) string {
	return mirAllocaArrayLine(reg, mirTypeI64(), countDigits)
}
func mirAllocaArrayI8Line(reg, countDigits string) string {
	return mirAllocaArrayLine(reg, mirTypeI8(), countDigits)
}

// §6 LLVM `phi` shape specialisations.

// Osty: mirPhiI8FromTwoLine / I32FromTwoLine / FloatFromTwoLine
func mirPhiI8FromTwoLine(reg, v1, l1, v2, l2 string) string {
	return "  " + reg + " = " + mirInstrPhi() + " i8 [ " + v1 + ", %" + l1 + " ], [ " + v2 + ", %" + l2 + " ]\n"
}
func mirPhiI32FromTwoLine(reg, v1, l1, v2, l2 string) string {
	return "  " + reg + " = " + mirInstrPhi() + " i32 [ " + v1 + ", %" + l1 + " ], [ " + v2 + ", %" + l2 + " ]\n"
}
func mirPhiFloatFromTwoLine(reg, v1, l1, v2, l2 string) string {
	return "  " + reg + " = " + mirInstrPhi() + " float [ " + v1 + ", %" + l1 + " ], [ " + v2 + ", %" + l2 + " ]\n"
}

// §6 `select` shape specialisations.

// Osty: mirSelectI8Line / I32Line / FloatLine
func mirSelectI8Line(reg, cond, l, r string) string {
	return "  " + reg + " = " + mirInstrSelect() + " i1 " + cond + ", i8 " + l + ", i8 " + r + "\n"
}
func mirSelectI32Line(reg, cond, l, r string) string {
	return "  " + reg + " = " + mirInstrSelect() + " i1 " + cond + ", i32 " + l + ", i32 " + r + "\n"
}
func mirSelectFloatLine(reg, cond, l, r string) string {
	return "  " + reg + " = " + mirInstrSelect() + " i1 " + cond + ", float " + l + ", float " + r + "\n"
}

// §6 extractvalue specialisations.

// Osty: mirExtractValueI64Line / I1Line / PtrLine / DoubleLine
func mirExtractValueI64Line(reg, aggTy, aggVal, idxDigits string) string {
	return mirExtractValueLine(reg, aggTy, aggVal, idxDigits)
}
func mirExtractValueI1Line(reg, aggTy, aggVal, idxDigits string) string {
	return mirExtractValueLine(reg, aggTy, aggVal, idxDigits)
}
func mirExtractValuePtrLine(reg, aggTy, aggVal, idxDigits string) string {
	return mirExtractValueLine(reg, aggTy, aggVal, idxDigits)
}
func mirExtractValueDoubleLine(reg, aggTy, aggVal, idxDigits string) string {
	return mirExtractValueLine(reg, aggTy, aggVal, idxDigits)
}

// §6 LLVM-text constant patterns.

// Osty: mirAlignAttr / mirZeroAttr / mirRangeAttrI64
func mirAlignAttr(alignDigits string) string { return "align " + alignDigits }
func mirZeroAttr() string                    { return "zeroinit" }
func mirRangeAttrI64(lo, hi string) string {
	return "range(i64 " + lo + ", " + hi + ")"
}

// §6 fastmath flag tokens.

// Osty: mirFastMathNNan / NInf / NSz / Arcp / Contract / Afn / Reassoc / Fast
func mirFastMathNNan() string     { return "nnan" }
func mirFastMathNInf() string     { return "ninf" }
func mirFastMathNSz() string      { return "nsz" }
func mirFastMathArcp() string     { return "arcp" }
func mirFastMathContract() string { return "contract" }
func mirFastMathAfn() string      { return "afn" }
func mirFastMathReassoc() string  { return "reassoc" }
func mirFastMathFast() string     { return "fast" }

// §6 nuw / nsw flag tokens.

// Osty: mirArithNUW / NSW / Exact
func mirArithNUW() string   { return "nuw" }
func mirArithNSW() string   { return "nsw" }
func mirArithExact() string { return "exact" }

// §6 GC-statepoint helpers (reserved).

// Osty: mirIntrinsicLLVMGCStatepoint / GCResult / GCRelocate / mirGCStatepointIDPlaceholder
func mirIntrinsicLLVMGCStatepoint() string { return "llvm.experimental.gc.statepoint" }
func mirIntrinsicLLVMGCResult() string     { return "llvm.experimental.gc.result" }
func mirIntrinsicLLVMGCRelocate() string   { return "llvm.experimental.gc.relocate" }
func mirGCStatepointIDPlaceholder() string { return "0" }

// §6 visibility / DLL-storage tokens.

// Osty: mirVisibilityDefault / Hidden / Protected / mirDLLImport / DLLExport
func mirVisibilityDefault() string   { return "default" }
func mirVisibilityHidden() string    { return "hidden" }
func mirVisibilityProtected() string { return "protected" }
func mirDLLImport() string           { return "dllimport" }
func mirDLLExport() string           { return "dllexport" }

// §6 LLVM module-preamble shape helpers.

// Osty: mirModuleHeaderTargetTriple / DataLayout / SourceFilename / ModuleAsm / SectionDirective
func mirModuleHeaderTargetTriple(triple string) string {
	return "target triple = \"" + triple + "\"\n"
}
func mirModuleHeaderDataLayout(layout string) string {
	return "target datalayout = \"" + layout + "\"\n"
}
func mirModuleHeaderSourceFilename(path string) string {
	return "source_filename = \"" + path + "\"\n"
}
func mirModuleHeaderModuleAsm(text string) string {
	return "module asm \"" + text + "\"\n"
}
func mirModuleSectionDirective(heading string) string {
	return "; ── " + heading + " ──\n"
}

// §6 runtime-declare line specialisations — additional shapes.
// (mirRuntimeDeclarePtrFromPtrLine / TwoPtr / ThreePtr / I64FromPtrLine /
// I1FromPtrLine / I1FromTwoPtrLine / VoidFromPtrLine /
// PtrFromI64Line / PtrFromPtrI64Line / I64NoArgsLine continue to live
// at their original sites earlier in this file.)

// Osty: mirRuntimeDeclareVoidFromTwoPtrLine / ThreePtr / PtrI64 / I64FromPtrI64 / PtrFromI64I64 / PtrNoArgs / I1NoArgs
func mirRuntimeDeclareVoidFromTwoPtrLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, ptr")
}
func mirRuntimeDeclareVoidFromThreePtrLine(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, ptr, ptr")
}
func mirRuntimeDeclareVoidFromPtrI64Line(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "ptr, i64")
}
func mirRuntimeDeclareI64FromPtrI64Line(sym string) string {
	return mirRuntimeDeclareLine("i64", sym, "ptr, i64")
}
func mirRuntimeDeclarePtrFromI64I64Line(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "i64, i64")
}
func mirRuntimeDeclarePtrNoArgsLine(sym string) string {
	return mirRuntimeDeclareLine("ptr", sym, "")
}
func mirRuntimeDeclareI1NoArgsLine(sym string) string {
	return mirRuntimeDeclareLine("i1", sym, "")
}

// §6 closure / dispatch-table builder helpers.

// Osty: mirClosureEnvLoadLine / FnPtrLoadLine / CallTypeLine
func mirClosureEnvLoadLine(reg, envSlot string) string {
	return mirLoadPtrLine(reg, envSlot)
}
func mirClosureFnPtrLoadLine(reg, fnSlot string) string {
	return mirLoadPtrLine(reg, fnSlot)
}
func mirClosureCallTypeLine(retTy, restParams string) string {
	if restParams == "" {
		return retTy + " (ptr)"
	}
	return retTy + " (ptr, " + restParams + ")"
}

// Osty: mirInterfaceVTableLoadLine / MethodPtrLoadLine / DataPtrLoadLine
func mirInterfaceVTableLoadLine(reg, fatPtr string) string {
	return mirLoadPtrLine(reg, fatPtr)
}
func mirInterfaceMethodPtrLoadLine(reg, methodSlot string) string {
	return mirLoadPtrLine(reg, methodSlot)
}
func mirInterfaceDataPtrLoadLine(reg, fatPtr, fatTy string) string {
	return "  " + reg + " = " + mirInstrExtractValue() + " " + fatTy + " " + fatPtr + ", 1\n"
}

// §6 dispatch-table emit shape helpers.

// Osty: mirVTableEntryGEPLine / ConstantArrayLine / EntryLine / EntryNullLine
func mirVTableEntryGEPLine(reg, arrSize, vtable, idxDigits string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " [" + arrSize + " x ptr], ptr " + vtable + ", i64 0, i64 " + idxDigits + "\n"
}
func mirVTableConstantArrayLine(arrSize, entries string) string {
	return "[" + arrSize + " x ptr] [" + entries + "]"
}
func mirVTableEntryLine(methodSym string) string { return "ptr @" + methodSym }
func mirVTableEntryNullLine() string             { return mirPtrNullLiteral() }

// §6 LLVM debug-info (DI) metadata literal shapes — reserved.

// Osty: mirDICompileUnit / DIFile / DISubprogram / DILocation / DIBasicTypeInt / DIBasicTypeFloat
func mirDICompileUnit(fileRef string) string {
	return "distinct !DICompileUnit(language: DW_LANG_C99, file: " + fileRef + ", producer: \"osty\", isOptimized: true, runtimeVersion: 0, emissionKind: FullDebug)"
}
func mirDIFile(filename, directory string) string {
	return "!DIFile(filename: \"" + filename + "\", directory: \"" + directory + "\")"
}
func mirDISubprogram(name, fileRef, lineDigits string) string {
	return "distinct !DISubprogram(name: \"" + name + "\", file: " + fileRef + ", line: " + lineDigits + ", isLocal: false, isDefinition: true)"
}
func mirDILocation(lineDigits, columnDigits, scopeRef string) string {
	return "!DILocation(line: " + lineDigits + ", column: " + columnDigits + ", scope: " + scopeRef + ")"
}
func mirDIBasicTypeInt(name, sizeBitsDigits string) string {
	return "!DIBasicType(name: \"" + name + "\", size: " + sizeBitsDigits + ", encoding: DW_ATE_signed)"
}
func mirDIBasicTypeFloat(name, sizeBitsDigits string) string {
	return "!DIBasicType(name: \"" + name + "\", size: " + sizeBitsDigits + ", encoding: DW_ATE_float)"
}

// §6 Common ABI / param-attr composite tokens.

// Osty: mirParamAttrReadOnlyNoAlias / WriteOnlyNoAlias / NoAliasNoCapture
func mirParamAttrReadOnlyNoAlias() string {
	return mirParamAttrReadOnly() + " " + mirParamAttrNoAlias()
}
func mirParamAttrWriteOnlyNoAlias() string {
	return mirParamAttrWriteOnly() + " " + mirParamAttrNoAlias()
}
func mirParamAttrNoAliasNoCapture() string {
	return mirParamAttrNoAlias() + " " + mirParamAttrNoCapture()
}

// §6 Function-attribute composite tokens.

// Osty: mirFnAttrInlineHotPure / ColdNoReturn / AlwaysInlineNoUnwind / PureWillReturn
func mirFnAttrInlineHotPure() string {
	return mirFnAttrInlineHint() + " " + mirFnAttrHot() + " " + mirFnAttrPure()
}
func mirFnAttrColdNoReturn() string {
	return mirFnAttrCold() + " " + mirFnAttrNoReturn() + " " + mirFnAttrNoUnwind()
}
func mirFnAttrAlwaysInlineNoUnwind() string {
	return mirFnAttrAlwaysInline() + " " + mirFnAttrNoUnwind()
}
func mirFnAttrPureWillReturn() string {
	return mirFnAttrPure() + " " + mirFnAttrWillReturn()
}

// §6 Common runtime-decl composite shapes.

// Osty: mirRuntimeDeclareReadOnly{Ptr,I64,I1}FromPtrLine
func mirRuntimeDeclareReadOnlyPtrFromPtrLine(sym string) string {
	return mirRuntimeDeclareMemoryRead("ptr", sym, "ptr")
}
func mirRuntimeDeclareReadOnlyI64FromPtrLine(sym string) string {
	return mirRuntimeDeclareMemoryRead("i64", sym, "ptr")
}
func mirRuntimeDeclareReadOnlyI1FromPtrLine(sym string) string {
	return mirRuntimeDeclareMemoryRead("i1", sym, "ptr")
}

// §6 Per-function arena helpers.

// Osty: mirFunctionDefineHeaderLine / FooterLine / EntryBlockHeaderLine / BlockHeaderLine
func mirFunctionDefineHeaderLine(linkage, retTy, name, params, attrs string) string {
	head := "define"
	if linkage != "" {
		head = head + " " + linkage
	}
	head = head + " " + retTy + " @" + name + "(" + params + ")"
	if attrs != "" {
		head = head + " " + attrs
	}
	return head + " {\n"
}
func mirFunctionDefineFooterLine() string { return "}\n" }
func mirEntryBlockHeaderLine() string     { return "entry:\n" }
func mirBlockHeaderLine(name string) string {
	return name + ":\n"
}

// §6 chunked emit-buffer flush helpers.

// Osty: mirEmitBuffer{Entry,GCRoot,Safepoint,LoopHeader}Line
func mirEmitBufferEntryLine() string      { return "; entry block\n" }
func mirEmitBufferGCRootLine() string     { return "; gc root snapshot\n" }
func mirEmitBufferSafepointLine() string  { return "; safepoint chunk\n" }
func mirEmitBufferLoopHeaderLine() string { return "; loop header\n" }

// §6 LLVM TBAA / alias.scope metadata literal shapes.

// Osty: mirTBAARootLine / ScalarTypeLine / StructTypeLine / TagLine
func mirTBAARootLine() string { return "!{!\"osty.tbaa.root\"}" }
func mirTBAAScalarTypeLine(name, parentRef string) string {
	return "!{!\"" + name + "\", " + parentRef + ", i64 0}"
}
func mirTBAAStructTypeLine(name, fieldsBody string) string {
	return "!{!\"" + name + "\", " + fieldsBody + "}"
}
func mirTBAATagLine(baseRef, accessRef, offsetDigits string) string {
	return "!{" + baseRef + ", " + accessRef + ", i64 " + offsetDigits + "}"
}

// §6 Profile-data / PGO metadata helpers.

// Osty: mirProfileBranchWeightsLine / FunctionEntryCountLine
func mirProfileBranchWeightsLine(trueCount, falseCount string) string {
	return "!{!\"branch_weights\", i32 " + trueCount + ", i32 " + falseCount + "}"
}
func mirProfileFunctionEntryCountLine(countDigits string) string {
	return "!{!\"function_entry_count\", i64 " + countDigits + "}"
}

// §6 Reserved sanitizer / instrumentation tokens.

// Osty: mirSanitizer{Address,Thread,Memory,HWAddress,UBSan}
func mirSanitizerAddress() string   { return "sanitize_address" }
func mirSanitizerThread() string    { return "sanitize_thread" }
func mirSanitizerMemory() string    { return "sanitize_memory" }
func mirSanitizerHWAddress() string { return "sanitize_hwaddress" }
func mirSanitizerUBSan() string     { return "sanitize_undefined" }

// §6 Stack-protector tokens.

// Osty: mirSSP{None,Basic,Strong,Required}
func mirSSPNone() string     { return "" }
func mirSSPBasic() string    { return "ssp" }
func mirSSPStrong() string   { return "sspstrong" }
func mirSSPRequired() string { return "sspreq" }

// §6 Common LLVM-text pattern compositions.

// Osty: mirOptionAggregateType / OptionPtrAggregateType / ResultAggregateType / InterfaceFatPointerType
func mirOptionAggregateType() string     { return "{ i64, i64 }" }
func mirOptionPtrAggregateType() string  { return "{ i64, ptr }" }
func mirResultAggregateType() string     { return mirOptionAggregateType() }
func mirInterfaceFatPointerType() string { return "{ ptr, ptr }" }

// Osty: mirChannel/List/Map/Set/String/BytesHandleType — semantic ptr aliases.
func mirChannelHandleType() string { return mirTypePtr() }
func mirListHandleType() string    { return mirTypePtr() }
func mirMapHandleType() string     { return mirTypePtr() }
func mirSetHandleType() string     { return mirTypePtr() }
func mirStringHandleType() string  { return mirTypePtr() }
func mirBytesHandleType() string   { return mirTypePtr() }

// §6 Numeric width tokens.

// Osty: mirSizeOf{Ptr,I64,I32,I16,I8,Double,Float}Bytes
func mirSizeOfPtrBytes() string    { return "8" }
func mirSizeOfI64Bytes() string    { return "8" }
func mirSizeOfI32Bytes() string    { return "4" }
func mirSizeOfI16Bytes() string    { return "2" }
func mirSizeOfI8Bytes() string     { return "1" }
func mirSizeOfDoubleBytes() string { return "8" }
func mirSizeOfFloatBytes() string  { return "4" }

// §6 channel / task-group runtime ABI shape helpers.

// Osty: mirCallValueChanNewLine / mirCallVoidChanSendLine / mirCallValueTaskGroupNewLine /
//
//	mirCallVoidTaskGroupCloseLine / mirCallValueTaskGroupSpawnLine /
//	mirCallValueHandleJoinLine / mirCallVoidHandleCancelLine
func mirCallValueChanNewLine(reg, bufSize, kind string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @osty_rt_chan_new(i64 " + bufSize + ", i64 " + kind + ")\n"
}

// (mirCallVoidChanSendLine continues to live at its original site
// earlier in this file.)
func mirCallValueTaskGroupNewLine(reg string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @osty_rt_taskgroup_new()\n"
}
func mirCallVoidTaskGroupCloseLine(group string) string {
	return "  " + mirInstrCallVoid() + " void @osty_rt_taskgroup_close(ptr " + group + ")\n"
}
func mirCallValueTaskGroupSpawnLine(reg, group, fnPtr, env string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @osty_rt_taskgroup_spawn(ptr " + group + ", ptr " + fnPtr + ", ptr " + env + ")\n"
}
func mirCallValueHandleJoinLine(reg, handle string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @osty_rt_handle_join(ptr " + handle + ")\n"
}
func mirCallVoidHandleCancelLine(handle string) string {
	return "  " + mirInstrCallVoid() + " void @osty_rt_handle_cancel(ptr " + handle + ")\n"
}

// §6 GC bridge shape helpers.

// Osty: mirCallValueGCAllocLine / mirCallVoidGCSafepointLine /
//
//	mirCallVoidGCBarrierLine / mirCallValueGCAllocatedBytesLine
func mirCallValueGCAllocLine(reg, sizeReg, kind string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @osty_rt_gc_alloc(i64 " + sizeReg + ", i64 " + kind + ")\n"
}
func mirCallVoidGCSafepointLine(slotsPtr, slotCount string) string {
	return "  " + mirInstrCallVoid() + " void @osty_rt_gc_safepoint(ptr " + slotsPtr + ", i64 " + slotCount + ")\n"
}
func mirCallVoidGCBarrierLine(targetPtr, valuePtr string) string {
	return "  " + mirInstrCallVoid() + " void @osty_rt_gc_barrier(ptr " + targetPtr + ", ptr " + valuePtr + ")\n"
}
func mirCallValueGCAllocatedBytesLine(reg string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @osty_rt_gc_allocated_bytes()\n"
}

// §6 panic / abort runtime helper call shapes.

// Osty: mirCallVoidPanicMessageLine / mirCallVoidUnreachableUncheckedLine /
//
//	mirCallVoidTodoLine / mirCallVoidAbortLine
func mirCallVoidPanicMessageLine(messagePtr string) string {
	return "  " + mirInstrCallVoid() + " void @osty_rt_panic(ptr " + messagePtr + ")\n"
}
func mirCallVoidUnreachableUncheckedLine() string {
	return "  " + mirInstrCallVoid() + " void @osty_rt_unreachable()\n"
}
func mirCallVoidTodoLine() string {
	return "  " + mirInstrCallVoid() + " void @osty_rt_todo()\n"
}
func mirCallVoidAbortLine() string {
	return "  " + mirInstrCallVoid() + " void @osty_rt_abort()\n"
}

// §6 standard math runtime call shapes.

// Osty: mirCallValueStdMath{Floor,Ceil,Round,Trunc,Mod}Line
func mirCallValueStdMathFloorLine(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @osty_rt_math_floor(double " + x + ")\n"
}
func mirCallValueStdMathCeilLine(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @osty_rt_math_ceil(double " + x + ")\n"
}
func mirCallValueStdMathRoundLine(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @osty_rt_math_round(double " + x + ")\n"
}
func mirCallValueStdMathTruncLine(reg, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @osty_rt_math_trunc(double " + x + ")\n"
}
func mirCallValueStdMathModLine(reg, a, b string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @osty_rt_math_mod(double " + a + ", double " + b + ")\n"
}

// §6 standard random runtime call shapes.

// Osty: mirCallValueStdRandom{I64,Double,RangeI64}Line
func mirCallValueStdRandomI64Line(reg string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @osty_rt_random_i64()\n"
}
func mirCallValueStdRandomDoubleLine(reg string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @osty_rt_random_double()\n"
}
func mirCallValueStdRandomRangeI64Line(reg, lo, hi string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @osty_rt_random_range_i64(i64 " + lo + ", i64 " + hi + ")\n"
}

// §6 LLVM-text aggregate-type composers.

// Osty: mirAggregateType2 / 3 / 4 / mirArrayType
func mirAggregateType2(a, b string) string {
	return "{ " + a + ", " + b + " }"
}
func mirAggregateType3(a, b, c string) string {
	return "{ " + a + ", " + b + ", " + c + " }"
}
func mirAggregateType4(a, b, c, d string) string {
	return "{ " + a + ", " + b + ", " + c + ", " + d + " }"
}
func mirArrayType(count, elem string) string {
	return "[" + count + " x " + elem + "]"
}

// §6 LLVM type-token compositions.

// Osty: mirOptionTypeForElem / mirResultTypeForElem
func mirOptionTypeForElem(elemTy string) string {
	if elemTy == "ptr" {
		return mirOptionPtrAggregateType()
	}
	return mirOptionAggregateType()
}
func mirResultTypeForElem(elemTy string) string {
	if elemTy == "ptr" {
		return mirOptionPtrAggregateType()
	}
	return mirOptionAggregateType()
}

// §6 LLVM-text constant patterns — additional shapes.

// Osty: mirAggregateConstantTwoPtr / I64I64 / I64Ptr
func mirAggregateConstantTwoPtr(aTy, a, bTy, b string) string {
	return "{ " + aTy + ", " + bTy + " } { " + aTy + " " + a + ", " + bTy + " " + b + " }"
}
func mirAggregateConstantI64I64(disc, payload string) string {
	return "{ i64, i64 } { i64 " + disc + ", i64 " + payload + " }"
}
func mirAggregateConstantI64Ptr(disc, payload string) string {
	return "{ i64, ptr } { i64 " + disc + ", ptr " + payload + " }"
}

// §6 closure-env / capture-list helpers.

// Osty: mirClosureEnvAllocLine / mirClosureCaptureFieldGEPLine / mirClosureFnPtrFieldGEPLine
func mirClosureEnvAllocLine(reg, sizeReg string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @osty_rt_gc_alloc(i64 " + sizeReg + ", i64 0)\n"
}
func mirClosureCaptureFieldGEPLine(reg, envTy, envPtr, idxDigits string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " " + envTy + ", ptr " + envPtr + ", i32 0, i32 " + idxDigits + "\n"
}
func mirClosureFnPtrFieldGEPLine(reg, closureTy, closurePtr string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " " + closureTy + ", ptr " + closurePtr + ", i32 0, i32 0\n"
}

// §6 struct field GEP / load shape helpers.

// Osty: mirStructFieldGEPLine / mirStructFieldLoadLine / mirStructFieldStoreLine
func mirStructFieldGEPLine(reg, structTy, structPtr, fieldIdxDigits string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " " + structTy + ", ptr " + structPtr + ", i32 0, i32 " + fieldIdxDigits + "\n"
}
func mirStructFieldLoadLine(slotReg, valueReg, structTy, structPtr, fieldIdxDigits, fieldTy string) string {
	return mirStructFieldGEPLine(slotReg, structTy, structPtr, fieldIdxDigits) +
		"  " + valueReg + " = " + mirInstrLoad() + " " + fieldTy + ", ptr " + slotReg + "\n"
}
func mirStructFieldStoreLine(slotReg, structTy, structPtr, fieldIdxDigits, fieldTy, value string) string {
	return mirStructFieldGEPLine(slotReg, structTy, structPtr, fieldIdxDigits) +
		"  " + mirInstrStore() + " " + fieldTy + " " + value + ", ptr " + slotReg + "\n"
}

// §6 LLVM-text size literal helpers.

// Osty: mirSizeLiteral{I64,I32,I16,I8,Double,Float,Ptr}Bytes
func mirSizeLiteralI64Bytes() string    { return mirIntLiteralI64(mirSizeOfI64Bytes()) }
func mirSizeLiteralI32Bytes() string    { return mirIntLiteralI64(mirSizeOfI32Bytes()) }
func mirSizeLiteralI16Bytes() string    { return mirIntLiteralI64(mirSizeOfI16Bytes()) }
func mirSizeLiteralI8Bytes() string     { return mirIntLiteralI64(mirSizeOfI8Bytes()) }
func mirSizeLiteralDoubleBytes() string { return mirIntLiteralI64(mirSizeOfDoubleBytes()) }
func mirSizeLiteralFloatBytes() string  { return mirIntLiteralI64(mirSizeOfFloatBytes()) }
func mirSizeLiteralPtrBytes() string    { return mirIntLiteralI64(mirSizeOfPtrBytes()) }

// §6 misc abi / linkage compositions.

// Osty: mirLinkageWithVisibility
func mirLinkageWithVisibility(linkage, visibility string) string {
	if visibility == mirVisibilityDefault() {
		return linkage
	}
	return linkage + " " + visibility
}

// §6 simple separator helpers.

// Osty: mirOpenBrace / CloseBrace / OpenBracket / CloseBracket / OpenParen / CloseParen / ToKeyword / LabelKeyword
func mirOpenBrace() string    { return "{" }
func mirCloseBrace() string   { return "}" }
func mirOpenBracket() string  { return "[" }
func mirCloseBracket() string { return "]" }
func mirOpenParen() string    { return "(" }
func mirCloseParen() string   { return ")" }
func mirToKeyword() string    { return "to" }
func mirLabelKeyword() string { return "label" }

// §6 numeric width / encoding tokens.

// Osty: mirIntWidthBits{I64,I32,I16,I8,I1} / mirFloatWidthBits{Double,Float} / mirIntTypeForBits
func mirIntWidthBitsI64() string      { return "64" }
func mirIntWidthBitsI32() string      { return "32" }
func mirIntWidthBitsI16() string      { return "16" }
func mirIntWidthBitsI8() string       { return "8" }
func mirIntWidthBitsI1() string       { return "1" }
func mirFloatWidthBitsDouble() string { return "64" }
func mirFloatWidthBitsFloat() string  { return "32" }
func mirIntTypeForBits(bits string) string {
	return "i" + bits
}

// §6 LLVM-text load / store with align tag specialisations.

// Osty: mirStoreI64WithAlignLine / DoubleWithAlignLine / PtrWithAlignLine /
//
//	mirLoadI64WithAlignLine / DoubleWithAlignLine / PtrWithAlignLine
func mirStoreI64WithAlignLine(val, slot, alignDigits string) string {
	return "  " + mirInstrStore() + " i64 " + val + ", ptr " + slot + ", align " + alignDigits + "\n"
}
func mirStoreDoubleWithAlignLine(val, slot, alignDigits string) string {
	return "  " + mirInstrStore() + " double " + val + ", ptr " + slot + ", align " + alignDigits + "\n"
}
func mirStorePtrWithAlignLine(val, slot, alignDigits string) string {
	return "  " + mirInstrStore() + " ptr " + val + ", ptr " + slot + ", align " + alignDigits + "\n"
}
func mirLoadI64WithAlignLine(reg, slot, alignDigits string) string {
	return "  " + reg + " = " + mirInstrLoad() + " i64, ptr " + slot + ", align " + alignDigits + "\n"
}
func mirLoadDoubleWithAlignLine(reg, slot, alignDigits string) string {
	return "  " + reg + " = " + mirInstrLoad() + " double, ptr " + slot + ", align " + alignDigits + "\n"
}
func mirLoadPtrWithAlignLine(reg, slot, alignDigits string) string {
	return "  " + reg + " = " + mirInstrLoad() + " ptr, ptr " + slot + ", align " + alignDigits + "\n"
}

// §6 !nonnull / !dereferenceable metadata-attached load shapes.

// Osty: mirLoadPtrNonNullLine / mirLoadPtrDereferenceableLine
func mirLoadPtrNonNullLine(reg, slot string) string {
	return "  " + reg + " = " + mirInstrLoad() + " ptr, ptr " + slot + ", !nonnull !{}\n"
}
func mirLoadPtrDereferenceableLine(reg, slot, bytesDigits string) string {
	return "  " + reg + " = " + mirInstrLoad() + " ptr, ptr " + slot + ", !dereferenceable !{i64 " + bytesDigits + "}\n"
}

// §6 ABI-arg list formation helpers.

// Osty: mirArgListI64I64 / I64I64I64 / PtrI64Ptr / FourPtr / PtrI1 / PtrPtrI64I64
func mirArgListI64I64(a, b string) string {
	return mirArgSlotI64(a) + ", " + mirArgSlotI64(b)
}
func mirArgListI64I64I64(a, b, c string) string {
	return mirArgSlotI64(a) + ", " + mirArgSlotI64(b) + ", " + mirArgSlotI64(c)
}
func mirArgListPtrI64Ptr(a, b, c string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotI64(b) + ", " + mirArgSlotPtr(c)
}
func mirArgListFourPtr(a, b, c, d string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotPtr(b) + ", " + mirArgSlotPtr(c) + ", " + mirArgSlotPtr(d)
}
func mirArgListPtrI1(a, b string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotI1(b)
}
func mirArgListPtrPtrI64I64(a, b, c, d string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotPtr(b) + ", " + mirArgSlotI64(c) + ", " + mirArgSlotI64(d)
}

// §6 typed-arg list with mixed sizes.

// Osty: mirArgListPtrI8 / PtrDouble / I64Ptr / I64I64Ptr
func mirArgListPtrI8(a, b string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotI8(b)
}
func mirArgListPtrDouble(a, b string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotDouble(b)
}
func mirArgListI64Ptr(a, b string) string {
	return mirArgSlotI64(a) + ", " + mirArgSlotPtr(b)
}
func mirArgListI64I64Ptr(a, b, c string) string {
	return mirArgSlotI64(a) + ", " + mirArgSlotI64(b) + ", " + mirArgSlotPtr(c)
}

// §6 LLVM-text indirection helpers.

// Osty: mirAddrOfGlobal / mirAddrOfLocal
func mirAddrOfGlobal(sym string) string { return "ptr @" + sym }
func mirAddrOfLocal(reg string) string  { return "ptr %" + reg }

// §6 LLVM-text comma-join helpers.

// Osty: mirJoinComma{Two,Three,Four,Five,Six}
func mirJoinCommaTwo(a, b string) string {
	return a + ", " + b
}
func mirJoinCommaThree(a, b, c string) string {
	return a + ", " + b + ", " + c
}
func mirJoinCommaFour(a, b, c, d string) string {
	return a + ", " + b + ", " + c + ", " + d
}
func mirJoinCommaFive(a, b, c, d, e string) string {
	return a + ", " + b + ", " + c + ", " + d + ", " + e
}
func mirJoinCommaSix(a, b, c, d, e, f string) string {
	return a + ", " + b + ", " + c + ", " + d + ", " + e + ", " + f
}

// §6 LLVM-text declare-line composers.

// Osty: mirRuntimeDeclareDoubleFromDoubleLine / TwoDoubleLine / DoubleFromI64Line /
//
//	I64FromDoubleLine / DoubleNoArgsLine / I64FromI64I64Line / VoidFromI64Line / I1FromI64I64Line
func mirRuntimeDeclareDoubleFromDoubleLine(sym string) string {
	return mirRuntimeDeclareLine("double", sym, "double")
}
func mirRuntimeDeclareDoubleFromTwoDoubleLine(sym string) string {
	return mirRuntimeDeclareLine("double", sym, "double, double")
}
func mirRuntimeDeclareDoubleFromI64Line(sym string) string {
	return mirRuntimeDeclareLine("double", sym, "i64")
}
func mirRuntimeDeclareI64FromDoubleLine(sym string) string {
	return mirRuntimeDeclareLine("i64", sym, "double")
}
func mirRuntimeDeclareDoubleNoArgsLine(sym string) string {
	return mirRuntimeDeclareLine("double", sym, "")
}
func mirRuntimeDeclareI64FromI64I64Line(sym string) string {
	return mirRuntimeDeclareLine("i64", sym, "i64, i64")
}
func mirRuntimeDeclareVoidFromI64Line(sym string) string {
	return mirRuntimeDeclareLine("void", sym, "i64")
}
func mirRuntimeDeclareI1FromI64I64Line(sym string) string {
	return mirRuntimeDeclareLine("i1", sym, "i64, i64")
}

// §6 ABI-shape composers for runtime "kind" tags.

// Osty: mirContainerKind{List,Map,Set,String,Bytes,Channel,ClosureEnv,Struct}
func mirContainerKindList() string       { return "0" }
func mirContainerKindMap() string        { return "1" }
func mirContainerKindSet() string        { return "2" }
func mirContainerKindString() string     { return "3" }
func mirContainerKindBytes() string      { return "4" }
func mirContainerKindChannel() string    { return "5" }
func mirContainerKindClosureEnv() string { return "6" }
func mirContainerKindStruct() string     { return "7" }

// §6 per-element kind tags.

// Osty: mirElementKind{I64,I32,I8,I1,Double,Ptr,String,Struct}
func mirElementKindI64() string    { return "0" }
func mirElementKindI32() string    { return "1" }
func mirElementKindI8() string     { return "2" }
func mirElementKindI1() string     { return "3" }
func mirElementKindDouble() string { return "4" }
func mirElementKindPtr() string    { return "5" }
func mirElementKindString() string { return "6" }
func mirElementKindStruct() string { return "7" }

// §6 discriminant / variant tag constants.

// Osty: mirDiscriminant{None,Some,Ok,Err}
func mirDiscriminantNone() string { return "0" }
func mirDiscriminantSome() string { return "1" }
func mirDiscriminantOk() string   { return "0" }
func mirDiscriminantErr() string  { return "1" }

// §6 boolean-literal tokens.

// Osty: mirBoolTrueLiteral / mirBoolFalseLiteral / mirBoolFromOsty
func mirBoolTrueLiteral() string  { return "1" }
func mirBoolFalseLiteral() string { return "0" }
func mirBoolFromOsty(b bool) string {
	if b {
		return mirBoolTrueLiteral()
	}
	return mirBoolFalseLiteral()
}

// §6 GEP helpers for struct member chains.

// Osty: mirGEPI64FieldZeroLine / mirGEPDoubleIndexLine / mirGEPNamedFieldLine
func mirGEPI64FieldZeroLine(reg, ty, basePtr string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " " + ty + ", ptr " + basePtr + ", i64 0\n"
}
func mirGEPDoubleIndexLine(reg, ty, basePtr, idx1, idx2 string) string {
	return "  " + reg + " = " + mirInstrGEPInBounds() + " " + ty + ", ptr " + basePtr + ", i64 " + idx1 + ", i64 " + idx2 + "\n"
}
func mirGEPNamedFieldLine(reg, ty, basePtr, fieldIdxDigits string) string {
	return mirStructFieldGEPLine(reg, ty, basePtr, fieldIdxDigits)
}

// §6 runtime-symbol composers.

// Osty: mirRtSymbol / mirRtListSymbol / mirRtMapSymbol / mirRtSetSymbol /
//       mirRtStringSymbol / mirRtBytesSymbol / mirRtChanSymbol /
//       mirRtTaskGroupSymbol / mirRtGCSymbol / mirRtTestSymbol /
//       mirRtMathSymbol / mirRtRandomSymbol / mirRtCancelSymbol
func mirRtSymbol(suffix string) string          { return "osty_rt_" + suffix }
func mirRtListSymbol(suffix string) string      { return "osty_rt_list_" + suffix }
func mirRtMapSymbol(suffix string) string       { return "osty_rt_map_" + suffix }
func mirRtSetSymbol(suffix string) string       { return "osty_rt_set_" + suffix }
func mirRtStringSymbol(suffix string) string    { return "osty_rt_strings_" + suffix }
func mirRtBytesSymbol(suffix string) string     { return "osty_rt_bytes_" + suffix }
func mirRtChanSymbol(suffix string) string      { return "osty_rt_chan_" + suffix }
func mirRtTaskGroupSymbol(suffix string) string { return "osty_rt_taskgroup_" + suffix }
func mirRtGCSymbol(suffix string) string        { return "osty_rt_gc_" + suffix }
func mirRtTestSymbol(suffix string) string      { return "osty_rt_test_" + suffix }
func mirRtMathSymbol(suffix string) string      { return "osty_rt_math_" + suffix }
func mirRtRandomSymbol(suffix string) string    { return "osty_rt_random_" + suffix }
func mirRtCancelSymbol(suffix string) string    { return "osty_rt_cancel_" + suffix }
func mirRtThreadSymbol(suffix string) string    { return "osty_rt_thread_" + suffix }
func mirRtBenchSymbol(suffix string) string     { return "osty_rt_bench_" + suffix }
func mirRtIOSymbol(suffix string) string        { return "osty_rt_io_" + suffix }
func mirRtJsonSymbol(suffix string) string      { return "osty_rt_json_" + suffix }
func mirRtFmtSymbol(suffix string) string       { return "osty_rt_fmt_" + suffix }

// §7 emit-pass per-block helpers.

// Osty: mirBlockSeparatorComment / mirBlockTraceLine / mirInstrTraceLine
func mirBlockSeparatorComment() string { return "; ---- next block ----\n" }
func mirBlockTraceLine(blockId, kind string) string {
	return "; block " + blockId + " (" + kind + ")\n"
}
func mirInstrTraceLine(kind, line, col string) string {
	return "; " + kind + " at " + line + ":" + col + "\n"
}

// §7 LLVM-text predicate composers.

// Osty: mirPredI64Eq / Ne / mirPredPtrEq / Ne / mirPredI1Eq
func mirPredI64Eq(a, b string) string {
	return mirInstrICmp() + " " + mirICmpEq() + " " + mirTypeI64() + " " + a + ", " + b
}
func mirPredI64Ne(a, b string) string {
	return mirInstrICmp() + " " + mirICmpNe() + " " + mirTypeI64() + " " + a + ", " + b
}
func mirPredPtrEq(a, b string) string {
	return mirInstrICmp() + " " + mirICmpEq() + " " + mirTypePtr() + " " + a + ", " + b
}
func mirPredPtrNe(a, b string) string {
	return mirInstrICmp() + " " + mirICmpNe() + " " + mirTypePtr() + " " + a + ", " + b
}
func mirPredI1Eq(a, b string) string {
	return mirInstrICmp() + " " + mirICmpEq() + " " + mirTypeI1() + " " + a + ", " + b
}

// §7 typed-store / typed-load shapes with !alias.scope metadata.

// Osty: mirStoreI64WithAliasScopeLine / DoubleWithAliasScopeLine / PtrWithAliasScopeLine /
//       LoadI64WithAliasScopeLine / DoubleWithAliasScopeLine / PtrWithAliasScopeLine
func mirStoreI64WithAliasScopeLine(val, slot, scopeRef string) string {
	return "  " + mirInstrStore() + " i64 " + val + ", ptr " + slot + ", !alias.scope " + scopeRef + "\n"
}
func mirStoreDoubleWithAliasScopeLine(val, slot, scopeRef string) string {
	return "  " + mirInstrStore() + " double " + val + ", ptr " + slot + ", !alias.scope " + scopeRef + "\n"
}
func mirStorePtrWithAliasScopeLine(val, slot, scopeRef string) string {
	return "  " + mirInstrStore() + " ptr " + val + ", ptr " + slot + ", !alias.scope " + scopeRef + "\n"
}
func mirLoadI64WithAliasScopeLine(reg, slot, scopeRef string) string {
	return "  " + reg + " = " + mirInstrLoad() + " i64, ptr " + slot + ", !alias.scope " + scopeRef + "\n"
}
func mirLoadDoubleWithAliasScopeLine(reg, slot, scopeRef string) string {
	return "  " + reg + " = " + mirInstrLoad() + " double, ptr " + slot + ", !alias.scope " + scopeRef + "\n"
}
func mirLoadPtrWithAliasScopeLine(reg, slot, scopeRef string) string {
	return "  " + reg + " = " + mirInstrLoad() + " ptr, ptr " + slot + ", !alias.scope " + scopeRef + "\n"
}

// §7 typed-store / typed-load shapes with !noalias metadata.

// Osty: mirStoreI64WithNoAliasLine / PtrWithNoAliasLine / LoadI64WithNoAliasLine / PtrWithNoAliasLine
func mirStoreI64WithNoAliasLine(val, slot, ref string) string {
	return "  " + mirInstrStore() + " i64 " + val + ", ptr " + slot + ", !noalias " + ref + "\n"
}
func mirStorePtrWithNoAliasLine(val, slot, ref string) string {
	return "  " + mirInstrStore() + " ptr " + val + ", ptr " + slot + ", !noalias " + ref + "\n"
}
func mirLoadI64WithNoAliasLine(reg, slot, ref string) string {
	return "  " + reg + " = " + mirInstrLoad() + " i64, ptr " + slot + ", !noalias " + ref + "\n"
}
func mirLoadPtrWithNoAliasLine(reg, slot, ref string) string {
	return "  " + reg + " = " + mirInstrLoad() + " ptr, ptr " + slot + ", !noalias " + ref + "\n"
}

// §7 LLVM access-group metadata helpers.

// Osty: mirLLVMAccessGroupRef / mirStoreI64WithAccessGroupLine /
//       DoubleWithAccessGroupLine / LoadI64WithAccessGroupLine /
//       DoubleWithAccessGroupLine
func mirLLVMAccessGroupRef(ref string) string {
	return "!access_group " + ref
}
func mirStoreI64WithAccessGroupLine(val, slot, ref string) string {
	return "  " + mirInstrStore() + " i64 " + val + ", ptr " + slot + ", !access_group " + ref + "\n"
}
func mirStoreDoubleWithAccessGroupLine(val, slot, ref string) string {
	return "  " + mirInstrStore() + " double " + val + ", ptr " + slot + ", !access_group " + ref + "\n"
}
func mirLoadI64WithAccessGroupLine(reg, slot, ref string) string {
	return "  " + reg + " = " + mirInstrLoad() + " i64, ptr " + slot + ", !access_group " + ref + "\n"
}
func mirLoadDoubleWithAccessGroupLine(reg, slot, ref string) string {
	return "  " + reg + " = " + mirInstrLoad() + " double, ptr " + slot + ", !access_group " + ref + "\n"
}

// §7 fixed-symbol composers.

// Osty: mirRtList* fixed-symbols
func mirRtListOOBAbortSymbol() string        { return mirRtListSymbol("oob_abort_v1") }
func mirRtListPopDiscardSymbol() string      { return mirRtListSymbol("pop_discard") }
func mirRtListIsEmptySymbol() string         { return mirRtListSymbol("is_empty") }
func mirRtListLenSymbolName() string         { return mirRtListSymbol("len") }
func mirRtListReverseSymbol() string         { return mirRtListSymbol("reverse") }
func mirRtListReversedSymbol() string        { return mirRtListSymbol("reversed") }
func mirRtListClearSymbol() string           { return mirRtListSymbol("clear") }
func mirRtListRemoveAtDiscardSymbol() string { return mirRtListSymbol("remove_at_discard") }

// Osty: mirRtMap* fixed-symbols
func mirRtMapNewSymbol() string       { return mirRtMapSymbol("new") }
func mirRtMapClearSymbol() string     { return mirRtMapSymbol("clear") }
func mirRtMapLenSymbolName() string   { return mirRtMapSymbol("len") }
func mirRtMapValuesSymbol() string    { return mirRtMapSymbol("values") }
func mirRtMapEntriesSymbol() string   { return mirRtMapSymbol("entries") }
func mirRtMapMergeWithSymbol() string { return mirRtMapSymbol("merge_with") }

// Osty: mirRtSet* fixed-symbols
func mirRtSetClearSymbol() string   { return mirRtSetSymbol("clear") }
func mirRtSetLenSymbolName() string { return mirRtSetSymbol("len") }
func mirRtSetToListSymbol() string  { return mirRtSetSymbol("to_list") }

// Osty: mirRtBytes* fixed-symbols
func mirRtBytesLenSymbolName() string { return mirRtBytesSymbol("len") }
func mirRtBytesIsEmptySymbol() string { return mirRtBytesSymbol("is_empty") }
func mirRtBytesGetSymbol() string     { return mirRtBytesSymbol("get") }

// Osty: mirRtString* fixed-symbols
func mirRtStringConcatSymbol() string    { return mirRtStringSymbol("Concat") }
func mirRtStringConcatNSymbol() string   { return mirRtStringSymbol("ConcatN") }
func mirRtStringDiffLinesSymbol() string { return mirRtStringSymbol("DiffLines") }

// Osty: mirRtCancel* fixed-symbols
func mirRtCancelCheckCancelledSymbol() string { return mirRtCancelSymbol("check_cancelled") }
func mirRtCancelIsCancelledSymbol() string    { return mirRtCancelSymbol("is_cancelled") }
func mirRtCancelCancelSymbol() string         { return mirRtCancelSymbol("cancel") }

// Osty: mirRtChan* fixed-symbols
func mirRtChanCloseSymbol() string    { return mirRtChanSymbol("close") }
func mirRtChanRecvSymbol() string     { return mirRtChanSymbol("recv") }
func mirRtChanSendSymbolName() string { return mirRtChanSymbol("send") }

// Osty: mirRtThread* fixed-symbols
func mirRtThreadYieldSymbol() string { return mirRtThreadSymbol("yield") }
func mirRtThreadSleepSymbol() string { return mirRtThreadSymbol("sleep") }
func mirRtThreadSpawnSymbol() string { return mirRtThreadSymbol("spawn") }

// Osty: mirRtBench* fixed-symbols
func mirRtBenchNowNanosSymbol() string { return mirRtBenchSymbol("now_nanos") }
func mirRtBenchTargetNsSymbol() string { return mirRtBenchSymbol("target_ns") }

// Osty: mirRtTest* fixed-symbols
func mirRtTestSnapshotSymbol() string     { return mirRtTestSymbol("snapshot") }
func mirRtTestAbortSymbol() string        { return mirRtTestSymbol("abort") }
func mirRtTestContextEnterSymbol() string { return mirRtTestSymbol("context_enter") }
func mirRtTestContextExitSymbol() string  { return mirRtTestSymbol("context_exit") }
func mirRtTestExpectOkSymbol() string     { return mirRtTestSymbol("expect_ok") }
func mirRtTestExpectErrorSymbol() string  { return mirRtTestSymbol("expect_error") }

// Osty: bare runtime symbols
func mirRtPanicSymbol() string       { return mirRtSymbol("panic") }
func mirRtUnreachableSymbol() string { return mirRtSymbol("unreachable") }
func mirRtTodoSymbol() string        { return mirRtSymbol("todo") }
func mirRtAbortSymbol() string       { return mirRtSymbol("abort") }

// Osty: option / result panic-helper symbols
func mirRtOptionUnwrapNoneSymbol() string { return mirRtSymbol("option_unwrap_none") }
func mirRtResultUnwrapErrSymbol() string  { return mirRtSymbol("result_unwrap_err") }
func mirRtExpectFailedSymbol() string     { return mirRtSymbol("expect_failed") }

// Osty: to-string runtime symbols
func mirRtIntToStringSymbol() string           { return mirRtSymbol("int_to_string") }
func mirRtFloatToStringSymbol() string         { return mirRtSymbol("float_to_string") }
func mirRtBoolToStringSymbol() string          { return mirRtSymbol("bool_to_string") }
func mirRtCharToStringSymbol() string          { return mirRtSymbol("char_to_string") }
func mirRtByteToStringSymbol() string          { return mirRtSymbol("byte_to_string") }
func mirRtListPrimitiveToStringSymbol() string { return mirRtListSymbol("primitive_to_string") }

// Osty: structured-concurrency runtime symbols
func mirRtParallelSymbol() string      { return mirRtSymbol("parallel") }
func mirRtRaceSymbol() string          { return mirRtSymbol("race") }
func mirRtTaskGroupRootSymbol() string { return mirRtSymbol("task_group") }

// §7 LLVM-text type-text composers.

// Osty: mirListTypeText / MapTypeText / SetTypeText / mirOptionTypeTextScalar /
//       OptionTypeTextPtr / mirResultTypeTextScalar / ResultTypeTextPtr
func mirListTypeText() string         { return mirTypePtr() }
func mirMapTypeText() string          { return mirTypePtr() }
func mirSetTypeText() string          { return mirTypePtr() }
func mirOptionTypeTextScalar() string { return mirOptionAggregateType() }
func mirOptionTypeTextPtr() string    { return mirOptionPtrAggregateType() }
func mirResultTypeTextScalar() string { return mirOptionAggregateType() }
func mirResultTypeTextPtr() string    { return mirOptionPtrAggregateType() }

// §7 LLVM-text discriminant probe / payload-extract shapes.

// Osty: mirOptionDiscProbeLine / PayloadProbeLine / mirOptionPtrDiscProbeLine /
//       PtrPayloadProbeLine / mirResultDiscProbeLine / PayloadProbeLine
func mirOptionDiscProbeLine(reg, aggReg string) string {
	return mirExtractValueLine(reg, mirOptionAggregateType(), aggReg, "0")
}
func mirOptionPayloadProbeLine(reg, aggReg string) string {
	return mirExtractValueLine(reg, mirOptionAggregateType(), aggReg, "1")
}
func mirOptionPtrDiscProbeLine(reg, aggReg string) string {
	return mirExtractValueLine(reg, mirOptionPtrAggregateType(), aggReg, "0")
}
func mirOptionPtrPayloadProbeLine(reg, aggReg string) string {
	return mirExtractValueLine(reg, mirOptionPtrAggregateType(), aggReg, "1")
}
func mirResultDiscProbeLine(reg, aggReg string) string {
	return mirOptionDiscProbeLine(reg, aggReg)
}
func mirResultPayloadProbeLine(reg, aggReg string) string {
	return mirOptionPayloadProbeLine(reg, aggReg)
}

// §7 LLVM-text Option / Result aggregate-construction shapes.

// Osty: mirOptionNoneAggregateLine / SomeDiscAggregateLine / SomePayloadAggregateLine /
//       mirResultOkDiscAggregateLine / ErrDiscAggregateLine
func mirOptionNoneAggregateLine(reg string) string {
	return mirInsertValueAggLine(reg, mirOptionAggregateType(), mirAggregateUndef(), mirTypeI64(), mirDiscriminantNone(), "0")
}
func mirOptionSomeDiscAggregateLine(reg string) string {
	return mirInsertValueAggLine(reg, mirOptionAggregateType(), mirAggregateUndef(), mirTypeI64(), mirDiscriminantSome(), "0")
}
func mirOptionSomePayloadAggregateLine(reg, prev, payload string) string {
	return mirInsertValueAggLine(reg, mirOptionAggregateType(), prev, mirTypeI64(), payload, "1")
}
func mirResultOkDiscAggregateLine(reg string) string {
	return mirInsertValueAggLine(reg, mirOptionAggregateType(), mirAggregateUndef(), mirTypeI64(), mirDiscriminantOk(), "0")
}
func mirResultErrDiscAggregateLine(reg string) string {
	return mirInsertValueAggLine(reg, mirOptionAggregateType(), mirAggregateUndef(), mirTypeI64(), mirDiscriminantErr(), "0")
}

// §7 LLVM-text panic-trap shape composers.

// Osty: mirPanicTrapLine / mirAbortTrapLine
func mirPanicTrapLine(sym, messagePtr string) string {
	return mirCallVoidPtrLine(sym, messagePtr) + mirUnreachableLine()
}
func mirAbortTrapLine(sym string) string {
	return mirCallVoidNoArgsLine(sym) + mirUnreachableLine()
}

// §7 LLVM-text Cond-branch shape composers.

// Osty: mirConditionalBranch3Line
func mirConditionalBranch3Line(cmpReg, ty, a, b, thenLbl, elseLbl string) string {
	return mirICmpLine(cmpReg, mirICmpEq(), ty, a, b) +
		mirBrCondLine(cmpReg, thenLbl, elseLbl)
}

// §7 LLVM-text loop-prelude shape helpers.

// Osty: mirLoopHeaderBlockLine / mirLoopBodyBlockLine / mirLoopExitBlockLine /
//       mirLoopLatchBlockLine
func mirLoopHeaderBlockLine(head string) string { return mirBlockHeaderLine(head) }
func mirLoopBodyBlockLine(body string) string   { return mirBlockHeaderLine(body) }
func mirLoopExitBlockLine(exit string) string   { return mirBlockHeaderLine(exit) }
func mirLoopLatchBlockLine(latch string) string { return mirBlockHeaderLine(latch) }

// §7 LLVM-text typed-Range-loop preamble helpers.

// Osty: mirRangeLoopInitLine / mirRangeLoopBoundLine
func mirRangeLoopInitLine(initReg, start string) string {
	return mirAddIntLine(initReg, mirTypeI64(), start, "0")
}
func mirRangeLoopBoundLine(boundReg, end string) string {
	return mirAddIntLine(boundReg, mirTypeI64(), end, "0")
}

// §7 vector-list snapshot composite helper.

// Osty: mirVectorListSnapshot2Line
func mirVectorListSnapshot2Line(dataReg, dataSym, lenReg, listReg, scopeRef string) string {
	return mirCallValueListDataNoAliasLine(dataReg, dataSym, listReg, scopeRef) +
		mirCallValueListLenWithScopeLine(lenReg, listReg, scopeRef)
}

// §8 monomorphisation key / sig composers.

// Osty: mirMonomorphKey / mirGenericInstanceKey / mirClosureSignatureKey
func mirMonomorphKey(base string, typeArgs []string) string {
	if len(typeArgs) == 0 {
		return base
	}
	parts := base + "::"
	first := true
	for _, a := range typeArgs {
		if !first {
			parts = parts + ","
		}
		parts = parts + a
		first = false
	}
	return parts
}
func mirGenericInstanceKey(base string, typeArgs []string) string {
	if len(typeArgs) == 0 {
		return base
	}
	parts := base + "["
	first := true
	for _, a := range typeArgs {
		if !first {
			parts = parts + ","
		}
		parts = parts + a
		first = false
	}
	return parts + "]"
}
func mirClosureSignatureKey(retTy string, paramTypes []string) string {
	parts := "closure[" + retTy + "]("
	first := true
	for _, p := range paramTypes {
		if !first {
			parts = parts + ", "
		}
		parts = parts + p
		first = false
	}
	return parts + ")"
}

// §8 LLVM-text fn-signature composers.

// Osty: mirFnSignatureType / mirFnPointerTypeWithEnv
func mirFnSignatureType(retTy string, paramTypes []string) string {
	parts := retTy + " ("
	first := true
	for _, p := range paramTypes {
		if !first {
			parts = parts + ", "
		}
		parts = parts + p
		first = false
	}
	return parts + ")"
}
func mirFnPointerTypeWithEnv(retTy string, restParamTypes []string) string {
	parts := retTy + " (ptr"
	for _, p := range restParamTypes {
		parts = parts + ", " + p
	}
	return parts + ")"
}

// §8 LLVM-text param-list shape helpers.

// Osty: mirParamSlot{Ptr,I64,I32,I1,I8,Double}
func mirParamSlotPtr(name string) string    { return "ptr %" + name }
func mirParamSlotI64(name string) string    { return "i64 %" + name }
func mirParamSlotI32(name string) string    { return "i32 %" + name }
func mirParamSlotI1(name string) string     { return "i1 %" + name }
func mirParamSlotI8(name string) string     { return "i8 %" + name }
func mirParamSlotDouble(name string) string { return "double %" + name }

// §8 LLVM-text typed-parameter list composers.

// Osty: mirParamListEnvPtr / EnvPtrAndOne / Two / Three
func mirParamListEnvPtr() string { return "ptr %env" }
func mirParamListEnvPtrAndOne(p1 string) string {
	return mirParamListEnvPtr() + ", " + p1
}
func mirParamListEnvPtrAndTwo(p1, p2 string) string {
	return mirParamListEnvPtr() + ", " + p1 + ", " + p2
}
func mirParamListEnvPtrAndThree(p1, p2, p3 string) string {
	return mirParamListEnvPtr() + ", " + p1 + ", " + p2 + ", " + p3
}

// §8 LLVM-text fn-name composers.

// Osty: mirOstyFnName / MethodName / ClosureName / VTableName / StringPoolName / FormatPoolName
func mirOstyFnName(packageName, fnName string) string {
	return "osty_" + packageName + "_" + fnName
}
func mirOstyMethodName(packageName, typeName, methodName string) string {
	return "osty_" + packageName + "_" + typeName + "_" + methodName
}
func mirOstyClosureName(idDigits string) string {
	return "osty_closure_" + idDigits
}
func mirOstyVTableName(typeName, interfaceName string) string {
	return "osty_vt_" + typeName + "__" + interfaceName
}
func mirOstyStringPoolName(idDigits string) string { return "@.str" + idDigits }
func mirOstyFormatPoolName(idDigits string) string { return "@.fmt" + idDigits }

// §8 LLVM-text local-name composers.

// Osty: mirLocalSlotName / ParamSlotName / TempRegName / BlockLabelName
// (mirBlockLabelName continues to live at its original site earlier in this file.)
func mirLocalSlotName(idDigits string) string { return "%l" + idDigits }
func mirParamSlotName(idDigits string) string { return "%p" + idDigits }
func mirTempRegName(idDigits string) string   { return "%t" + idDigits }

// §8 LLVM-text alias-scope metadata helpers.

// Osty: mirAliasScopeMetadataNode / ListMetadataNode / Reference / mirNoAliasReference
func mirAliasScopeMetadataNode(scopeRef string) string {
	return "!{!" + scopeRef + "}"
}
func mirAliasScopeListMetadataNode(refs string) string {
	return "!{" + refs + "}"
}
func mirAliasScopeReference(ref string) string {
	return "!alias.scope " + ref
}
func mirNoAliasReference(ref string) string {
	return "!noalias " + ref
}

// §8 LLVM-text comma-joined / space-joined list helpers.

// Osty: mirJoinCommaList / mirJoinSpaceList
func mirJoinCommaList(parts []string) string {
	joined := ""
	first := true
	for _, p := range parts {
		if !first {
			joined = joined + ", "
		}
		joined = joined + p
		first = false
	}
	return joined
}
func mirJoinSpaceList(parts []string) string {
	joined := ""
	first := true
	for _, p := range parts {
		if !first {
			joined = joined + " "
		}
		joined = joined + p
		first = false
	}
	return joined
}

// §8 LLVM-text constant-array body composers.

// Osty: mirConstantArrayBody / OfList / mirConstantStructBody / OfList
func mirConstantArrayBody(entries string) string {
	return "[" + entries + "]"
}
func mirConstantArrayBodyOfList(entries []string) string {
	return "[" + mirJoinCommaList(entries) + "]"
}
func mirConstantStructBody(fields string) string {
	return "{" + fields + "}"
}
func mirConstantStructBodyOfList(fields []string) string {
	return "{" + mirJoinCommaList(fields) + "}"
}

// §8 LLVM-text typed-constant fragment helpers.

// Osty: mirTypedConstFragment / I64 / I1 / Ptr / Double
func mirTypedConstFragment(ty, val string) string {
	return ty + " " + val
}
func mirTypedConstFragmentI64(val string) string {
	return mirTypedConstFragment(mirTypeI64(), val)
}
func mirTypedConstFragmentI1(val string) string {
	return mirTypedConstFragment(mirTypeI1(), val)
}
func mirTypedConstFragmentPtr(val string) string {
	return mirTypedConstFragment(mirTypePtr(), val)
}
func mirTypedConstFragmentDouble(val string) string {
	return mirTypedConstFragment(mirTypeDouble(), val)
}

// §8 LLVM-text comments-by-purpose helpers.

// Osty: mirComment{Safepoint,NoSafepoint,Vectorize,NoVectorize,Parallel,Inline,NoInline,Hot,Cold}
func mirCommentSafepoint() string   { return "  ; safepoint\n" }
func mirCommentNoSafepoint() string { return "  ; no-safepoint\n" }
func mirCommentVectorize() string   { return "  ; vectorize-eligible\n" }
func mirCommentNoVectorize() string { return "  ; no-vectorize\n" }
func mirCommentParallel() string    { return "  ; parallel\n" }
func mirCommentInline() string      { return "  ; inline-hint\n" }
func mirCommentNoInline() string    { return "  ; no-inline\n" }
func mirCommentHot() string         { return "  ; hot\n" }
func mirCommentCold() string        { return "  ; cold\n" }

// §8 LLVM-text parameter-binding helpers.

// Osty: mirParamBinding{Ptr,I64,I1,Double}
func mirParamBindingPtr(paramSlot, argName string) string {
	return mirAllocaPtrLine(paramSlot) +
		"  " + mirInstrStore() + " ptr " + argName + ", ptr " + paramSlot + "\n"
}
func mirParamBindingI64(paramSlot, argName string) string {
	return mirAllocaI64Line(paramSlot) +
		"  " + mirInstrStore() + " i64 " + argName + ", ptr " + paramSlot + "\n"
}
func mirParamBindingI1(paramSlot, argName string) string {
	return mirAllocaI1Line(paramSlot) +
		"  " + mirInstrStore() + " i1 " + argName + ", ptr " + paramSlot + "\n"
}
func mirParamBindingDouble(paramSlot, argName string) string {
	return mirAllocaDoubleLine(paramSlot) +
		"  " + mirInstrStore() + " double " + argName + ", ptr " + paramSlot + "\n"
}

// §8 GC root array setup composite helpers.

// Osty: mirGCRootSlotsAllocaWithCommentLine / mirGCRootSafepointWithCommentLine
func mirGCRootSlotsAllocaWithCommentLine(slotsPtr, countDigits string) string {
	return "  ; gc roots\n" + mirGCRootSlotsAllocaLine(slotsPtr, countDigits)
}
func mirGCRootSafepointWithCommentLine(slotsPtr, slotCount string) string {
	return mirCommentSafepoint() + mirCallVoidGCSafepointLine(slotsPtr, slotCount)
}

// §8 LLVM-text builder-buffer helpers.

// Osty: mirBuildLines{2,3,4,5}
func mirBuildLines2(a, b string) string             { return a + b }
func mirBuildLines3(a, b, c string) string          { return a + b + c }
func mirBuildLines4(a, b, c, d string) string       { return a + b + c + d }
func mirBuildLines5(a, b, c, d, e string) string    { return a + b + c + d + e }

// §8 LLVM-text type alias helpers.

// Osty: mirTypeAliasLine / mirNamedAggregateType
func mirTypeAliasLine(name, body string) string {
	return "%" + name + " = type " + body + "\n"
}
func mirNamedAggregateType(name string) string {
	return "%" + name
}

// §8 LLVM-text emit-pass abbreviation helpers.

// Osty: mirEmitVoidCallStmt / mirEmitValueCallStmt
func mirEmitVoidCallStmt(sym, args string) string {
	return mirInstrCallVoid() + " void @" + sym + "(" + args + ")"
}
func mirEmitValueCallStmt(reg, retTy, sym, args string) string {
	return reg + " = " + mirInstrCall() + " " + retTy + " @" + sym + "(" + args + ")"
}

// §8 LLVM-text noreturn-call shape helpers.

// Osty: mirCallNoReturnVoidLine / NoArgsAttrLine
func mirCallNoReturnVoidLine(sym, args string) string {
	return "  " + mirEmitVoidCallStmt(sym, args) + " #1\n"
}
func mirCallNoReturnVoidNoArgsAttrLine(sym string) string {
	return "  " + mirInstrCallVoid() + " void @" + sym + "() #1\n"
}

// §8 LLVM-text named-md-tuple helpers.

// Osty: mirNamedMDTuple / mirNamedMDDistinctTuple / mirAnonymousMDTuple
func mirNamedMDTuple(name, entries string) string {
	return "!" + name + " = !{" + entries + "}\n"
}
func mirNamedMDDistinctTuple(name, entries string) string {
	return "!" + name + " = distinct !{" + entries + "}\n"
}
func mirAnonymousMDTuple(entries string) string {
	return "!{" + entries + "}"
}

// §9 LLVM-text emit-pass section markers.

// Osty: mirSection{Declares,Globals,Functions,Metadata,Prelude,Epilogue}
func mirSectionDeclares() string  { return "; declares\n" }
func mirSectionGlobals() string   { return "; globals\n" }
func mirSectionFunctions() string { return "; functions\n" }
func mirSectionMetadata() string  { return "; metadata\n" }
func mirSectionPrelude() string   { return "; prelude\n" }
func mirSectionEpilogue() string  { return "; epilogue\n" }

// §9 LLVM-text reference-encoding helpers.

// Osty: mirGlobalRef / mirRegRef / mirMDRef
func mirGlobalRef(sym string) string { return "@" + sym }
func mirRegRef(name string) string   { return "%" + name }
func mirMDRef(name string) string    { return "!" + name }

// §9 LLVM-text label-emit shape helpers.

// Osty: mirLabelHeaderLine / mirJumpToLabelLine
func mirLabelHeaderLine(name string) string { return name + ":\n" }
func mirJumpToLabelLine(dst string) string  { return mirBrLabelLine(dst) }

// §9 LLVM-text typed-arg list extension helpers.

// Osty: mirAppendArg{Ptr,I64,I1,Double,I8,I32}
func mirAppendArgPtr(prev, reg string) string    { return prev + ", ptr " + reg }
func mirAppendArgI64(prev, reg string) string    { return prev + ", i64 " + reg }
func mirAppendArgI1(prev, reg string) string     { return prev + ", i1 " + reg }
func mirAppendArgDouble(prev, reg string) string { return prev + ", double " + reg }
func mirAppendArgI8(prev, reg string) string     { return prev + ", i8 " + reg }
func mirAppendArgI32(prev, reg string) string    { return prev + ", i32 " + reg }

// §9 LLVM-text list-len / list-isEmpty common shape composers.

// Osty: mirCallListLenLine / mirCallListIsEmptyLine / mirCallMapLenLine /
//       mirCallSetLenLine / mirCallBytesLenLine
func mirCallListLenLine(reg, listReg string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + mirRtListLenSymbolName() + "(ptr " + listReg + ")\n"
}
func mirCallListIsEmptyLine(reg, listReg string) string {
	return "  " + reg + " = " + mirInstrCall() + " i1 @" + mirRtListIsEmptySymbol() + "(ptr " + listReg + ")\n"
}
func mirCallMapLenLine(reg, mapReg string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + mirRtMapLenSymbolName() + "(ptr " + mapReg + ")\n"
}
func mirCallSetLenLine(reg, setReg string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + mirRtSetLenSymbolName() + "(ptr " + setReg + ")\n"
}
func mirCallBytesLenLine(reg, bytesReg string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + mirRtBytesLenSymbolName() + "(ptr " + bytesReg + ")\n"
}

// §9 LLVM-text loop-iter increment shape helpers.

// Osty: mirIncrementLoopCounterLine / mirDecrementLoopCounterLine
func mirIncrementLoopCounterLine(nextReg, iReg string) string {
	return mirAddIntLine(nextReg, mirTypeI64(), iReg, "1")
}
func mirDecrementLoopCounterLine(nextReg, iReg string) string {
	return mirSubIntLine(nextReg, mirTypeI64(), iReg, "1")
}

// §9 LLVM-text typed-load-store with align tag composite helpers.

// Osty: mirLoadStoreLine / I64Line / I1Line / PtrLine / DoubleLine
func mirLoadStoreLine(tmpReg, ty, src, dst string) string {
	return "  " + tmpReg + " = " + mirInstrLoad() + " " + ty + ", ptr " + src + "\n" +
		"  " + mirInstrStore() + " " + ty + " " + tmpReg + ", ptr " + dst + "\n"
}
func mirLoadStoreI64Line(tmpReg, src, dst string) string {
	return mirLoadStoreLine(tmpReg, mirTypeI64(), src, dst)
}
func mirLoadStoreI1Line(tmpReg, src, dst string) string {
	return mirLoadStoreLine(tmpReg, mirTypeI1(), src, dst)
}
func mirLoadStorePtrLine(tmpReg, src, dst string) string {
	return mirLoadStoreLine(tmpReg, mirTypePtr(), src, dst)
}
func mirLoadStoreDoubleLine(tmpReg, src, dst string) string {
	return mirLoadStoreLine(tmpReg, mirTypeDouble(), src, dst)
}

// §9 LLVM-text label-helper specialisations.

// Osty: mirLabel{Ok,Err,Done,LoopHead,LoopBody,LoopExit,LoopLatch,MatchArmPrefix,MatchExit,IfThen,IfElse,IfEnd,OptionSome,OptionNone,ResultOk,ResultErr}
func mirLabelOk() string             { return "ok" }
func mirLabelErr() string            { return "err" }
func mirLabelDone() string           { return "done" }
func mirLabelLoopHead() string       { return "loop.head" }
func mirLabelLoopBody() string       { return "loop.body" }
func mirLabelLoopExit() string       { return "loop.exit" }
func mirLabelLoopLatch() string      { return "loop.latch" }
func mirLabelMatchArmPrefix() string { return "match.arm" }
func mirLabelMatchExit() string      { return "match.exit" }
func mirLabelIfThen() string         { return "if.then" }
func mirLabelIfElse() string         { return "if.else" }
func mirLabelIfEnd() string          { return "if.end" }
func mirLabelOptionSome() string     { return "option.some" }
func mirLabelOptionNone() string     { return "option.none" }
func mirLabelResultOk() string       { return "result.ok" }
func mirLabelResultErr() string      { return "result.err" }

// §10 expanded fixed-symbol composers — Bytes runtime long-tail.

// Osty: mirRtBytes{IndexOf,LastIndexOf,Split,Join,Concat,Repeat,Replace,ReplaceAll,
//       TrimLeft,TrimRight,Trim,TrimSpace,ToUpper,ToLower,ToHex,Slice,Contains,
//       StartsWith,EndsWith}Symbol
func mirRtBytesIndexOfSymbol() string     { return mirRtBytesSymbol("index_of") }
func mirRtBytesLastIndexOfSymbol() string { return mirRtBytesSymbol("last_index_of") }
func mirRtBytesSplitSymbol() string       { return mirRtBytesSymbol("split") }
func mirRtBytesJoinSymbol() string        { return mirRtBytesSymbol("join") }
func mirRtBytesConcatSymbol() string      { return mirRtBytesSymbol("concat") }
func mirRtBytesRepeatSymbol() string      { return mirRtBytesSymbol("repeat") }
func mirRtBytesReplaceSymbol() string     { return mirRtBytesSymbol("replace") }
func mirRtBytesReplaceAllSymbol() string  { return mirRtBytesSymbol("replace_all") }
func mirRtBytesTrimLeftSymbol() string    { return mirRtBytesSymbol("trim_left") }
func mirRtBytesTrimRightSymbol() string   { return mirRtBytesSymbol("trim_right") }
func mirRtBytesTrimSymbol() string        { return mirRtBytesSymbol("trim") }
func mirRtBytesTrimSpaceSymbol() string   { return mirRtBytesSymbol("trim_space") }
func mirRtBytesToUpperSymbol() string     { return mirRtBytesSymbol("to_upper") }
func mirRtBytesToLowerSymbol() string     { return mirRtBytesSymbol("to_lower") }
func mirRtBytesToHexSymbol() string       { return mirRtBytesSymbol("to_hex") }
func mirRtBytesSliceSymbol() string       { return mirRtBytesSymbol("slice") }
func mirRtBytesContainsSymbol() string    { return mirRtBytesSymbol("contains") }
func mirRtBytesStartsWithSymbol() string  { return mirRtBytesSymbol("starts_with") }
func mirRtBytesEndsWithSymbol() string    { return mirRtBytesSymbol("ends_with") }

// §10 String runtime symbol composers.

// Osty: mirRtString{Chars,Bytes,ByteLen,ToUpper,ToLower,IsValidInt,ToInt,
//       IsValidFloat,ToFloat,Count,Slice,SplitInto,NthSegment,IndexOf,
//       LastIndexOf,Contains,StartsWith,EndsWith,Trim,Split,Join,Replace,
//       Repeat,Hash,IsEmpty,Len}Symbol
func mirRtStringCharsSymbol() string        { return mirRtStringSymbol("Chars") }
func mirRtStringBytesSymbol() string        { return mirRtStringSymbol("Bytes") }
func mirRtStringByteLenSymbol() string      { return mirRtStringSymbol("ByteLen") }
func mirRtStringToUpperSymbol() string      { return mirRtStringSymbol("ToUpper") }
func mirRtStringToLowerSymbol() string      { return mirRtStringSymbol("ToLower") }
func mirRtStringIsValidIntSymbol() string   { return mirRtStringSymbol("IsValidInt") }
func mirRtStringToIntSymbol() string        { return mirRtStringSymbol("ToInt") }
func mirRtStringIsValidFloatSymbol() string { return mirRtStringSymbol("IsValidFloat") }
func mirRtStringToFloatSymbol() string      { return mirRtStringSymbol("ToFloat") }
func mirRtStringCountSymbol() string        { return mirRtStringSymbol("Count") }
func mirRtStringSliceSymbol() string        { return mirRtStringSymbol("Slice") }
func mirRtStringSplitIntoSymbol() string    { return mirRtStringSymbol("SplitInto") }
func mirRtStringNthSegmentSymbol() string   { return mirRtStringSymbol("NthSegment") }
func mirRtStringIndexOfSymbol() string      { return mirRtStringSymbol("IndexOf") }
func mirRtStringLastIndexOfSymbol() string  { return mirRtStringSymbol("LastIndexOf") }
func mirRtStringContainsSymbol() string     { return mirRtStringSymbol("Contains") }
func mirRtStringStartsWithSymbol() string   { return mirRtStringSymbol("StartsWith") }
func mirRtStringEndsWithSymbol() string     { return mirRtStringSymbol("EndsWith") }
func mirRtStringTrimSymbol() string         { return mirRtStringSymbol("Trim") }
func mirRtStringSplitSymbol() string        { return mirRtStringSymbol("Split") }
func mirRtStringJoinSymbol() string         { return mirRtStringSymbol("Join") }
func mirRtStringReplaceSymbol() string      { return mirRtStringSymbol("Replace") }
func mirRtStringRepeatSymbol() string       { return mirRtStringSymbol("Repeat") }
func mirRtStringHashSymbol() string         { return mirRtStringSymbol("Hash") }
func mirRtStringIsEmptySymbol() string      { return mirRtStringSymbol("IsEmpty") }
func mirRtStringLenSymbol() string          { return mirRtStringSymbol("Len") }

// §10 Map runtime symbol composers.

// Osty: mirRtMap{Insert,Get,Remove,Keys,ContainsKey,Update,MapValues,RetainIf,GetOr}Symbol
func mirRtMapInsertSymbol() string      { return mirRtMapSymbol("insert") }
func mirRtMapGetSymbol() string         { return mirRtMapSymbol("get") }
func mirRtMapRemoveSymbol() string      { return mirRtMapSymbol("remove") }
func mirRtMapKeysSymbol() string        { return mirRtMapSymbol("keys") }
func mirRtMapContainsKeySymbol() string { return mirRtMapSymbol("contains_key") }
func mirRtMapUpdateSymbol() string      { return mirRtMapSymbol("update") }
func mirRtMapMapValuesSymbol() string   { return mirRtMapSymbol("map_values") }
func mirRtMapRetainIfSymbol() string    { return mirRtMapSymbol("retain_if") }
func mirRtMapGetOrSymbol() string       { return mirRtMapSymbol("get_or") }

// §10 Set runtime symbol composers.

// Osty: mirRtSet{Add,Remove,Contains,IsEmpty,Union,Intersection,Difference}Symbol
func mirRtSetAddSymbol() string          { return mirRtSetSymbol("add") }
func mirRtSetRemoveSymbol() string       { return mirRtSetSymbol("remove") }
func mirRtSetContainsSymbol() string     { return mirRtSetSymbol("contains") }
func mirRtSetIsEmptySymbol() string      { return mirRtSetSymbol("is_empty") }
func mirRtSetUnionSymbol() string        { return mirRtSetSymbol("union") }
func mirRtSetIntersectionSymbol() string { return mirRtSetSymbol("intersection") }
func mirRtSetDifferenceSymbol() string   { return mirRtSetSymbol("difference") }

// §10 List runtime symbol composers.

// Osty: mirRtList{New,Push,Pop,Insert,Contains,Map,Filter,Fold,Slice,Sorted,Extend,GroupBy}Symbol
func mirRtListNewSymbol() string      { return mirRtListSymbol("new") }
func mirRtListPushSymbol() string     { return mirRtListSymbol("push") }
func mirRtListPopSymbol() string      { return mirRtListSymbol("pop") }
func mirRtListInsertSymbol() string   { return mirRtListSymbol("insert") }
func mirRtListContainsSymbol() string { return mirRtListSymbol("contains") }
func mirRtListMapSymbol() string      { return mirRtListSymbol("map") }
func mirRtListFilterSymbol() string   { return mirRtListSymbol("filter") }
func mirRtListFoldSymbol() string     { return mirRtListSymbol("fold") }
func mirRtListSliceSymbol() string    { return mirRtListSymbol("slice") }
func mirRtListSortedSymbol() string   { return mirRtListSymbol("sorted") }
func mirRtListExtendSymbol() string   { return mirRtListSymbol("extend") }
func mirRtListGroupBySymbol() string  { return mirRtListSymbol("group_by") }

// §10 Channel runtime symbol composers.

// Osty: mirRtChan{New,IsClosed,Len,Cap}Symbol
func mirRtChanNewSymbol() string      { return mirRtChanSymbol("new") }
func mirRtChanIsClosedSymbol() string { return mirRtChanSymbol("is_closed") }
func mirRtChanLenSymbol() string      { return mirRtChanSymbol("len") }
func mirRtChanCapSymbol() string      { return mirRtChanSymbol("cap") }

// §10 GC runtime symbol composers.

// Osty: mirRtGC{Alloc,Safepoint,Barrier,AllocatedBytes}Symbol / mirRtGCDebugAllocatedBytesTotalSymbol
func mirRtGCAllocSymbol() string                    { return mirRtGCSymbol("alloc") }
func mirRtGCSafepointSymbol() string                { return mirRtGCSymbol("safepoint") }
func mirRtGCBarrierSymbol() string                  { return mirRtGCSymbol("barrier") }
func mirRtGCAllocatedBytesSymbol() string           { return mirRtGCSymbol("allocated_bytes") }
func mirRtGCDebugAllocatedBytesTotalSymbol() string { return "osty_gc_debug_allocated_bytes_total" }

// §10 Math runtime symbol composers.

// Osty: mirRtMath{Floor,Ceil,Round,Trunc,Mod,Pow,Sqrt,Abs,Sin,Cos,Tan,Exp,Log,Log2,Log10,Min,Max}Symbol
func mirRtMathFloorSymbol() string { return mirRtMathSymbol("floor") }
func mirRtMathCeilSymbol() string  { return mirRtMathSymbol("ceil") }
func mirRtMathRoundSymbol() string { return mirRtMathSymbol("round") }
func mirRtMathTruncSymbol() string { return mirRtMathSymbol("trunc") }
func mirRtMathModSymbol() string   { return mirRtMathSymbol("mod") }
func mirRtMathPowSymbol() string   { return mirRtMathSymbol("pow") }
func mirRtMathSqrtSymbol() string  { return mirRtMathSymbol("sqrt") }
func mirRtMathAbsSymbol() string   { return mirRtMathSymbol("abs") }
func mirRtMathSinSymbol() string   { return mirRtMathSymbol("sin") }
func mirRtMathCosSymbol() string   { return mirRtMathSymbol("cos") }
func mirRtMathTanSymbol() string   { return mirRtMathSymbol("tan") }
func mirRtMathExpSymbol() string   { return mirRtMathSymbol("exp") }
func mirRtMathLogSymbol() string   { return mirRtMathSymbol("log") }
func mirRtMathLog2Symbol() string  { return mirRtMathSymbol("log2") }
func mirRtMathLog10Symbol() string { return mirRtMathSymbol("log10") }
func mirRtMathMinSymbol() string   { return mirRtMathSymbol("min") }
func mirRtMathMaxSymbol() string   { return mirRtMathSymbol("max") }

// §10 Random runtime symbol composers.

// Osty: mirRtRandom{I64,Double,RangeI64,Shuffle,Choice}Symbol
func mirRtRandomI64Symbol() string      { return mirRtRandomSymbol("i64") }
func mirRtRandomDoubleSymbol() string   { return mirRtRandomSymbol("double") }
func mirRtRandomRangeI64Symbol() string { return mirRtRandomSymbol("range_i64") }
func mirRtRandomShuffleSymbol() string  { return mirRtRandomSymbol("shuffle") }
func mirRtRandomChoiceSymbol() string   { return mirRtRandomSymbol("choice") }

// §10 Json runtime symbol composers.

// Osty: mirRtJson{Parse,Stringify}Symbol
func mirRtJsonParseSymbol() string     { return mirRtJsonSymbol("parse") }
func mirRtJsonStringifySymbol() string { return mirRtJsonSymbol("stringify") }

// §10 Fmt runtime symbol composers.

// Osty: mirRtFmt{Sprintf,Printf,PrintLine}Symbol
func mirRtFmtSprintfSymbol() string   { return mirRtFmtSymbol("sprintf") }
func mirRtFmtPrintfSymbol() string    { return mirRtFmtSymbol("printf") }
func mirRtFmtPrintLineSymbol() string { return mirRtFmtSymbol("println") }

// §10 IO runtime symbol composers.

// Osty: mirRtIO{Read,Write,ReadLine,ReadAll,Flush}Symbol
func mirRtIOReadSymbol() string     { return mirRtIOSymbol("read") }
func mirRtIOWriteSymbol() string    { return mirRtIOSymbol("write") }
func mirRtIOReadLineSymbol() string { return mirRtIOSymbol("read_line") }
func mirRtIOReadAllSymbol() string  { return mirRtIOSymbol("read_all") }
func mirRtIOFlushSymbol() string    { return mirRtIOSymbol("flush") }

// §11 LLVM-text typed-call shape composers — generic call shapes.

// Osty: mirCallI64FromPtrI64Line / NoArgsLine
// (mirCallI64FromPtrLine / TwoPtrLine continue to live at their
// original sites earlier in this file.)
func mirCallI64FromPtrI64Line(reg, sym, a, b string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + sym + "(ptr " + a + ", i64 " + b + ")\n"
}
func mirCallI64NoArgsLine(reg, sym string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + sym + "()\n"
}

// Osty: mirCallPtrFromThreePtrLine / PtrI64Line / PtrI64I64Line / I64Line / I64I64Line / NoArgsLine
// (mirCallPtrFromPtrLine / TwoPtrLine continue to live at their
// original sites earlier in this file.)
func mirCallPtrFromThreePtrLine(reg, sym, a, b, c string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @" + sym + "(ptr " + a + ", ptr " + b + ", ptr " + c + ")\n"
}
func mirCallPtrFromPtrI64Line(reg, sym, a, b string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @" + sym + "(ptr " + a + ", i64 " + b + ")\n"
}
func mirCallPtrFromPtrI64I64Line(reg, sym, a, b, c string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @" + sym + "(ptr " + a + ", i64 " + b + ", i64 " + c + ")\n"
}
func mirCallPtrFromI64Line(reg, sym, a string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @" + sym + "(i64 " + a + ")\n"
}
func mirCallPtrFromI64I64Line(reg, sym, a, b string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @" + sym + "(i64 " + a + ", i64 " + b + ")\n"
}
func mirCallPtrNoArgsLine(reg, sym string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @" + sym + "()\n"
}

// Osty: mirCallI1NoArgsLine
// (mirCallI1FromPtrLine / TwoPtrLine continue to live at their
// original sites earlier in this file.)
func mirCallI1NoArgsLine(reg, sym string) string {
	return "  " + reg + " = " + mirInstrCall() + " i1 @" + sym + "()\n"
}

// Osty: mirCallVoidFromPtrI64Line / I64Line / NoArgsTrampolineLine
// (mirCallVoidFromPtrLine / TwoPtrLine continue to live at their
// original sites earlier in this file.)
func mirCallVoidFromPtrI64Line(sym, a, b string) string {
	return "  " + mirInstrCallVoid() + " void @" + sym + "(ptr " + a + ", i64 " + b + ")\n"
}
func mirCallVoidFromI64Line(sym, a string) string {
	return "  " + mirInstrCallVoid() + " void @" + sym + "(i64 " + a + ")\n"
}
func mirCallVoidNoArgsTrampolineLine(sym string) string {
	return "  " + mirInstrCallVoid() + " void @" + sym + "()\n"
}

// §11 LLVM-text typed-runtime call shapes for double-result paths.

// Osty: mirCallDoubleFromDoubleLine / TwoDoubleLine / I64Line / NoArgsLine / mirCallI64FromDoubleLine
func mirCallDoubleFromDoubleLine(reg, sym, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @" + sym + "(double " + x + ")\n"
}
func mirCallDoubleFromTwoDoubleLine(reg, sym, a, b string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @" + sym + "(double " + a + ", double " + b + ")\n"
}
func mirCallDoubleFromI64Line(reg, sym, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @" + sym + "(i64 " + x + ")\n"
}
func mirCallDoubleNoArgsLine(reg, sym string) string {
	return "  " + reg + " = " + mirInstrCall() + " double @" + sym + "()\n"
}
func mirCallI64FromDoubleLine(reg, sym, x string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + sym + "(double " + x + ")\n"
}

// §11 LLVM-text typed insertvalue shape composers.

// Osty: mirInsertValueI64Line / PtrLine / I1Line / DoubleLine
func mirInsertValueI64Line(reg, aggTy, prev, val, fieldIdx string) string {
	return mirInsertValueAggLine(reg, aggTy, prev, mirTypeI64(), val, fieldIdx)
}
func mirInsertValuePtrLine(reg, aggTy, prev, val, fieldIdx string) string {
	return mirInsertValueAggLine(reg, aggTy, prev, mirTypePtr(), val, fieldIdx)
}
func mirInsertValueI1Line(reg, aggTy, prev, val, fieldIdx string) string {
	return mirInsertValueAggLine(reg, aggTy, prev, mirTypeI1(), val, fieldIdx)
}
func mirInsertValueDoubleLine(reg, aggTy, prev, val, fieldIdx string) string {
	return mirInsertValueAggLine(reg, aggTy, prev, mirTypeDouble(), val, fieldIdx)
}

// §11 LLVM-text per-target-feature attribute helpers.

// Osty: mirTargetFeaturesAttr / mirTargetCpuAttr / mirNoFramePointerAttr / mirAllFramePointerAttr
func mirTargetFeaturesAttr(featuresWithPlus string) string {
	return "\"target-features\"=\"" + featuresWithPlus + "\""
}
func mirTargetCpuAttr(cpu string) string {
	return "\"target-cpu\"=\"" + cpu + "\""
}
func mirNoFramePointerAttr() string  { return "\"frame-pointer\"=\"none\"" }
func mirAllFramePointerAttr() string { return "\"frame-pointer\"=\"all\"" }

// §11 LLVM-text per-loop metadata helpers.

// Osty: mirLoopMDLine / mirLoopBranchWithMDLine / mirLoopIDNodeBody
func mirLoopMDLine(ref string) string {
	return ", !llvm.loop " + ref
}
func mirLoopBranchWithMDLine(cond, header, exit, mdRef string) string {
	return "  " + mirTermBr() + " i1 " + cond + ", label %" + header + ", label %" + exit + mirLoopMDLine(mdRef) + "\n"
}
func mirLoopIDNodeBody(selfRef, body string) string {
	if body == "" {
		return "!{" + selfRef + "}"
	}
	return "!{" + selfRef + ", " + body + "}"
}

// §11 LLVM-text named-metadata global-list helpers.

// Osty: mirModuleFlagsList / mirIdentList / mirCompilerInfoLine
func mirModuleFlagsList(entries string) string {
	return "!llvm.module.flags = !{" + entries + "}\n"
}
func mirIdentList(entries string) string {
	return "!llvm.ident = !{" + entries + "}\n"
}
func mirCompilerInfoLine(version string) string {
	return "!{!\"Osty " + version + "\"}"
}

// §11 LLVM-text fastmath-flag composite helpers.

// Osty: mirFastMathFlagsForArith
func mirFastMathFlagsForArith() string {
	return mirFastMathNNan() + " " + mirFastMathNInf() + " " + mirFastMathNSz() + " " +
		mirFastMathContract() + " " + mirFastMathArcp() + " " + mirFastMathReassoc()
}

// §11 LLVM-text small-helper shapes.

// Osty: mirNullPtrConst / Zero{I64,I32,Double}Const / One{I64,I32,Double}Const / True{I1}Const / FalseI1Const
func mirNullPtrConst() string     { return mirPtrNullLiteral() }
func mirZeroI64Const() string     { return "i64 0" }
func mirZeroI32Const() string     { return "i32 0" }
func mirZeroDoubleConst() string  { return "double 0.0" }
func mirOneI64Const() string      { return "i64 1" }
func mirOneI32Const() string      { return "i32 1" }
func mirOneDoubleConst() string   { return "double 1.0" }
func mirTrueI1Const() string      { return "i1 1" }
func mirFalseI1Const() string     { return "i1 0" }

// §11 LLVM-text minus-one constant tokens.

// Osty: mirMinusOne{I64,I32,I8}Const
func mirMinusOneI64Const() string { return "i64 -1" }
func mirMinusOneI32Const() string { return "i32 -1" }
func mirMinusOneI8Const() string  { return "i8 -1" }

// §11 LLVM-text Range-loop / List-iter init / step / bound shape helpers.

// Osty: mirRangeInclusiveBoundLine / mirRangeStepLine / mirRangeIterationCondLine / mirListIterCondLine / StepLine
func mirRangeInclusiveBoundLine(reg, end string) string {
	return mirAddI64ImmediateLine(reg, end, "1")
}
func mirRangeStepLine(reg, i, step string) string {
	return mirAddIntLine(reg, mirTypeI64(), i, step)
}
func mirRangeIterationCondLine(reg, i, bound string) string {
	return mirICmpI64SltLine(reg, i, bound)
}
func mirListIterCondLine(reg, i, lenReg string) string {
	return mirICmpI64SltLine(reg, i, lenReg)
}
func mirListIterStepLine(reg, i string) string {
	return mirAddI64ImmediateLine(reg, i, "1")
}

// §11 LLVM-text noreturn-decl line composer.

// Osty: mirRuntimeDeclareNoReturnVoid / mirRuntimeDeclareNoReturnVoidNoArgs
func mirRuntimeDeclareNoReturnVoid(sym, args string) string {
	return "declare void @" + sym + "(" + args + ") #1\n"
}
func mirRuntimeDeclareNoReturnVoidNoArgs(sym string) string {
	return mirRuntimeDeclareNoReturnVoid(sym, "")
}

// §11 LLVM-text per-call cold-attribute markers.

// Osty: mirCallColdAttr / mirCallHotAttr
func mirCallColdAttr() string { return mirFnAttrCold() }
func mirCallHotAttr() string  { return mirFnAttrHot() }

// §11 LLVM-text typed-comparison composite shapes.

// Osty: mirICmpZeroI64Line / NonZeroI64Line / NullPtrLine / NonNullPtrLine
func mirICmpZeroI64Line(reg, a string) string {
	return mirICmpI64EqLine(reg, a, "0")
}
func mirICmpNonZeroI64Line(reg, a string) string {
	return mirICmpI64NeLine(reg, a, "0")
}
func mirICmpNullPtrLine(reg, a string) string {
	return mirICmpPtrEqLine(reg, a, "null")
}
func mirICmpNonNullPtrLine(reg, a string) string {
	return mirICmpPtrNeLine(reg, a, "null")
}

// §12 task / select / race runtime symbol composers.

// Osty: mirRtTask{Spawn,GroupSpawn,HandleJoin,GroupCancel,GroupIsCancelled,Race,CollectAll}Symbol
func mirRtTaskSpawnSymbol() string             { return mirRtSymbol("task_spawn") }
func mirRtTaskGroupSpawnSymbol() string        { return mirRtSymbol("task_group_spawn") }
func mirRtTaskHandleJoinSymbol() string        { return mirRtSymbol("task_handle_join") }
func mirRtTaskGroupCancelSymbol() string       { return mirRtSymbol("task_group_cancel") }
func mirRtTaskGroupIsCancelledSymbol() string  { return mirRtSymbol("task_group_is_cancelled") }
func mirRtTaskRaceSymbol() string              { return mirRtSymbol("task_race") }
func mirRtTaskCollectAllSymbol() string        { return mirRtSymbol("task_collect_all") }

// Osty: mirRtSelect{,Recv,Timeout,Default}Symbol / mirRtSelectSendBytesV1Symbol
func mirRtSelectSymbol() string             { return mirRtSymbol("select") }
func mirRtSelectRecvSymbol() string         { return mirRtSymbol("select_recv") }
func mirRtSelectTimeoutSymbol() string      { return mirRtSymbol("select_timeout") }
func mirRtSelectDefaultSymbol() string      { return mirRtSymbol("select_default") }
func mirRtSelectSendBytesV1Symbol() string  { return mirRtSymbol("select_send_bytes_v1") }

// Osty: mirRtListSetBytesV1Symbol / GetBytesV1Symbol / SetSuffixSymbol / GetSuffixSymbol /
//       MapKeysSortedSuffixSymbol / SelectSendSuffixSymbol
func mirRtListSetBytesV1Symbol() string                   { return mirRtListSymbol("set_bytes_v1") }
func mirRtListGetBytesV1Symbol() string                   { return mirRtListSymbol("get_bytes_v1") }
func mirRtListSetSuffixSymbol(suffix string) string       { return mirRtListSymbol("set_" + suffix) }
func mirRtListGetSuffixSymbol(suffix string) string       { return mirRtListSymbol("get_" + suffix) }
func mirRtMapKeysSortedSuffixSymbol(suffix string) string { return mirRtMapSymbol("keys_sorted_" + suffix) }
func mirRtSelectSendSuffixSymbol(suffix string) string    { return mirRtSymbol("select_send_" + suffix) }

// §12 LLVM-text typed-arg slot composer specialisations.

// Osty: mirArgSlot{I64,I32,I8,I1}Imm / PtrNull / PtrSym
func mirArgSlotI64Imm(digits string) string { return mirIntLiteralI64(digits) }
func mirArgSlotI32Imm(digits string) string { return mirIntLiteralI32(digits) }
func mirArgSlotI8Imm(digits string) string  { return mirIntLiteralI8(digits) }
func mirArgSlotI1Imm(digits string) string  { return mirIntLiteralI1(digits) }
func mirArgSlotPtrNull() string             { return mirPtrNullLiteral() }
func mirArgSlotPtrSym(sym string) string    { return "ptr @" + sym }

// §12 LLVM-text emit-shape composite helpers.

// Osty: mirAlloca2Line / mirAllocaInit{I64,I1,Ptr,Double}Line
func mirAlloca2Line(slotReg, ty, val string) string {
	return mirAllocaSingleLine(slotReg, ty) +
		"  " + mirInstrStore() + " " + ty + " " + val + ", ptr " + slotReg + "\n"
}
func mirAllocaInitI64Line(slotReg, val string) string {
	return mirAlloca2Line(slotReg, mirTypeI64(), val)
}
func mirAllocaInitI1Line(slotReg, val string) string {
	return mirAlloca2Line(slotReg, mirTypeI1(), val)
}
func mirAllocaInitPtrLine(slotReg, val string) string {
	return mirAlloca2Line(slotReg, mirTypePtr(), val)
}
func mirAllocaInitDoubleLine(slotReg, val string) string {
	return mirAlloca2Line(slotReg, mirTypeDouble(), val)
}

// §12 LLVM-text typed-load + typed-cast composite helpers.

// Osty: mirLoadAndZExtI8ToI64Line / mirLoadAndSExtI8ToI64Line /
//       mirLoadAndZExtI1ToI64Line / mirLoadI64AndTruncToI8Line /
//       mirLoadI64AndTruncToI1Line
func mirLoadAndZExtI8ToI64Line(loadReg, zextReg, slot string) string {
	return mirLoadI8Line(loadReg, slot) + mirZExtI8ToI64Line(zextReg, loadReg)
}
func mirLoadAndSExtI8ToI64Line(loadReg, sextReg, slot string) string {
	return mirLoadI8Line(loadReg, slot) + mirSExtI8ToI64Line(sextReg, loadReg)
}
func mirLoadAndZExtI1ToI64Line(loadReg, zextReg, slot string) string {
	return mirLoadI1Line(loadReg, slot) + mirZExtI1ToI64Line(zextReg, loadReg)
}
func mirLoadI64AndTruncToI8Line(loadReg, truncReg, slot string) string {
	return mirLoadI64Line(loadReg, slot) + mirTruncI64ToI8Line(truncReg, loadReg)
}
func mirLoadI64AndTruncToI1Line(loadReg, truncReg, slot string) string {
	return mirLoadI64Line(loadReg, slot) + mirTruncI64ToI1Line(truncReg, loadReg)
}

// §12 LLVM-text canonical-block emit helpers.

// Osty: mirEntryEpilogueBlocksLine / mirIfElseEndBlocksLine
func mirEntryEpilogueBlocksLine(entryBody, exitLbl, exitBody string) string {
	return mirEntryBlockHeaderLine() + entryBody +
		mirBrLabelLine(exitLbl) + mirBlockHeaderLine(exitLbl) + exitBody
}
func mirIfElseEndBlocksLine(thenLbl, elseLbl, endLbl string) string {
	return mirBlockHeaderLine(thenLbl) + mirBlockHeaderLine(elseLbl) + mirBlockHeaderLine(endLbl)
}

// §12 LLVM-text generic-fn-attr group composer.

// Osty: mirAttributeGroupLine / mirAttributeGroupNoReturnCold / mirAttributeGroupHotPure
func mirAttributeGroupLine(idDigits, attrs string) string {
	return "attributes #" + idDigits + " = { " + attrs + " }\n"
}
func mirAttributeGroupNoReturnCold(idDigits string) string {
	return mirAttributeGroupLine(idDigits, mirFnAttrColdNoReturn())
}
func mirAttributeGroupHotPure(idDigits string) string {
	return mirAttributeGroupLine(idDigits, mirFnAttrInlineHotPure())
}

// §12 LLVM-text RawPtr / fp bitcast composite helpers.

// Osty: mirCastI64ToPtrLine / mirCastPtrToI64Line / mirCastDoubleToI64Line / mirCastI64ToDoubleLine
func mirCastI64ToPtrLine(reg, val string) string {
	return mirIntToPtrLine(reg, mirTypeI64(), val)
}
func mirCastPtrToI64Line(reg, val string) string {
	return mirPtrToIntLine(reg, val, mirTypeI64())
}
func mirCastDoubleToI64Line(reg, val string) string {
	return mirBitcastLine(reg, mirTypeDouble(), val, mirTypeI64())
}
func mirCastI64ToDoubleLine(reg, val string) string {
	return mirBitcastLine(reg, mirTypeI64(), val, mirTypeDouble())
}

// §12 LLVM-text reference-helpers (semantic aliases).

// Osty: mirRefToGlobal / mirRefToMD / mirRefToReg
func mirRefToGlobal(sym string) string { return mirGlobalRef(sym) }
func mirRefToMD(name string) string    { return mirMDRef(name) }
func mirRefToReg(name string) string   { return mirRegRef(name) }

// §12 LLVM-text typed-zero / typed-one constant composites.

// Osty: mirTypedZeroForLine / mirTypedOneForLine
func mirTypedZeroForLine(ty string) string { return ty + " " + mirZeroOfType(ty) }
func mirTypedOneForLine(ty string) string  { return ty + " " + mirOneOfType(ty) }

// §12 LLVM-text canonical zero-aggregate constants.

// Osty: mirOptionNoneConst / mirOptionPtrNoneConst / mirResultOkUnitConst / mirResultErrPtrConst
func mirOptionNoneConst() string {
	return mirAggregateConstantI64I64(mirDiscriminantNone(), "0")
}
func mirOptionPtrNoneConst() string {
	return mirAggregateConstantI64Ptr(mirDiscriminantNone(), "null")
}
func mirResultOkUnitConst() string {
	return mirAggregateConstantI64I64(mirDiscriminantOk(), "0")
}
func mirResultErrPtrConst(errPtr string) string {
	return mirAggregateConstantI64Ptr(mirDiscriminantErr(), errPtr)
}

// §13 LLVM-text struct-field projection helpers.

// Osty: mirStructFieldI32GEPLine / mirAggregateFieldExtractI32
func mirStructFieldI32GEPLine(reg, structTy, basePtr, fieldIdxDigits string) string {
	return mirStructFieldGEPLine(reg, structTy, basePtr, fieldIdxDigits)
}
func mirAggregateFieldExtractI32(reg, aggTy, agg, fieldIdx string) string {
	return mirExtractValueLine(reg, aggTy, agg, fieldIdx)
}

// §13 LLVM-text vtable-slot computation helpers.

// Osty: mirVTableMethodLoadLine
func mirVTableMethodLoadLine(slotReg, fnReg, arrSize, vtable, idxDigits string) string {
	return mirVTableEntryGEPLine(slotReg, arrSize, vtable, idxDigits) +
		mirLoadPtrLine(fnReg, slotReg)
}

// §13 LLVM-text typed-aggregate field-store / field-load composite shapes.

// Osty: mirAggregateFieldStore{I64,Ptr}Line / mirAggregateFieldLoad{I64,Ptr}Line
func mirAggregateFieldStoreI64Line(slotReg, structTy, basePtr, fieldIdxDigits, val string) string {
	return mirStructFieldGEPLine(slotReg, structTy, basePtr, fieldIdxDigits) +
		"  " + mirInstrStore() + " i64 " + val + ", ptr " + slotReg + "\n"
}
func mirAggregateFieldStorePtrLine(slotReg, structTy, basePtr, fieldIdxDigits, val string) string {
	return mirStructFieldGEPLine(slotReg, structTy, basePtr, fieldIdxDigits) +
		"  " + mirInstrStore() + " ptr " + val + ", ptr " + slotReg + "\n"
}
func mirAggregateFieldLoadI64Line(slotReg, valReg, structTy, basePtr, fieldIdxDigits string) string {
	return mirStructFieldGEPLine(slotReg, structTy, basePtr, fieldIdxDigits) +
		mirLoadI64Line(valReg, slotReg)
}
func mirAggregateFieldLoadPtrLine(slotReg, valReg, structTy, basePtr, fieldIdxDigits string) string {
	return mirStructFieldGEPLine(slotReg, structTy, basePtr, fieldIdxDigits) +
		mirLoadPtrLine(valReg, slotReg)
}

// §13 LLVM-text closure-emit composite helpers.

// Osty: mirClosureEnvFieldStoreLine / mirClosureEnvFieldLoadLine
func mirClosureEnvFieldStoreLine(slotReg, envTy, envPtr, fieldIdxDigits, capturedPtr string) string {
	return mirClosureCaptureFieldGEPLine(slotReg, envTy, envPtr, fieldIdxDigits) +
		"  " + mirInstrStore() + " ptr " + capturedPtr + ", ptr " + slotReg + "\n"
}
func mirClosureEnvFieldLoadLine(slotReg, valReg, envTy, envPtr, fieldIdxDigits string) string {
	return mirClosureCaptureFieldGEPLine(slotReg, envTy, envPtr, fieldIdxDigits) +
		mirLoadPtrLine(valReg, slotReg)
}

// §13 Sized-aggregate alloca + memset composite helpers.

// Osty: mirAllocaWithSizeAndZeroLine
func mirAllocaWithSizeAndZeroLine(reg, ty, sizeBytesDigits string) string {
	return mirAllocaSingleLine(reg, ty) +
		mirCallVoidLLVMMemsetLine(reg, "0", sizeBytesDigits, "false")
}

// §13 Common composite shapes for runtime-call sequencing.

// Osty: mirSpillThenCallVoidLine / mirCallThenStore{I64,Ptr}Line
func mirSpillThenCallVoidLine(slotReg, ty, val, sym, args string) string {
	return mirAlloca2Line(slotReg, ty, val) +
		"  " + mirInstrCallVoid() + " void @" + sym + "(" + args + ")\n"
}
func mirCallThenStoreI64Line(reg, sym, args, slot string) string {
	return "  " + reg + " = " + mirInstrCall() + " i64 @" + sym + "(" + args + ")\n" +
		"  " + mirInstrStore() + " i64 " + reg + ", ptr " + slot + "\n"
}
func mirCallThenStorePtrLine(reg, sym, args, slot string) string {
	return "  " + reg + " = " + mirInstrCall() + " ptr @" + sym + "(" + args + ")\n" +
		"  " + mirInstrStore() + " ptr " + reg + ", ptr " + slot + "\n"
}

// §13 LLVM-text typed bool-not / short-circuit shape helpers.

// Osty: mirBoolNotLine / mirBoolAndShortCircuitLine / mirBoolOrShortCircuitLine
func mirBoolNotLine(reg, a string) string {
	return mirXorIntLine(reg, mirTypeI1(), a, "true")
}
func mirBoolAndShortCircuitLine(cmpReg, lhs, falseLbl, mergeLbl string) string {
	return mirICmpI1NeLine(cmpReg, lhs, "0") +
		mirBrCondLine(cmpReg, mergeLbl, falseLbl) +
		mirBlockHeaderLine(mergeLbl)
}
func mirBoolOrShortCircuitLine(cmpReg, lhs, trueLbl, mergeLbl string) string {
	return mirICmpI1NeLine(cmpReg, lhs, "0") +
		mirBrCondLine(cmpReg, trueLbl, mergeLbl) +
		mirBlockHeaderLine(mergeLbl)
}

// §13 Common GEP-then-load / GEP-then-store composite shapes.

// Osty: mirGEPThenLoad{I64,Ptr}Line / mirGEPThenStore{I64,Ptr}Line
func mirGEPThenLoadI64Line(slotReg, valReg, ty, basePtr, idxDigits string) string {
	return mirGEPI64StrideLine(slotReg, basePtr, idxDigits) +
		mirLoadI64Line(valReg, slotReg)
}
func mirGEPThenLoadPtrLine(slotReg, valReg, ty, basePtr, idxDigits string) string {
	return mirGEPPtrStrideLine(slotReg, basePtr, idxDigits) +
		mirLoadPtrLine(valReg, slotReg)
}
func mirGEPThenStoreI64Line(slotReg, ty, basePtr, idxDigits, val string) string {
	return mirGEPI64StrideLine(slotReg, basePtr, idxDigits) +
		"  " + mirInstrStore() + " i64 " + val + ", ptr " + slotReg + "\n"
}
func mirGEPThenStorePtrLine(slotReg, ty, basePtr, idxDigits, val string) string {
	return mirGEPPtrStrideLine(slotReg, basePtr, idxDigits) +
		"  " + mirInstrStore() + " ptr " + val + ", ptr " + slotReg + "\n"
}

// §14 LLVM-text aggregate-construction composite shapes.

// Osty: mirOptionSomeI64BuildLine / mirOptionSomePtrBuildLine /
//       mirResultOk{I64,Ptr}BuildLine / mirResultErrPtrBuildLine
func mirOptionSomeI64BuildLine(stepReg, fullReg, payload string) string {
	return mirInsertValueI64Line(stepReg, mirOptionAggregateType(), mirAggregateUndef(), mirDiscriminantSome(), "0") +
		mirInsertValueI64Line(fullReg, mirOptionAggregateType(), stepReg, payload, "1")
}
func mirOptionSomePtrBuildLine(stepReg, fullReg, payloadPtr string) string {
	return mirInsertValueI64Line(stepReg, mirOptionPtrAggregateType(), mirAggregateUndef(), mirDiscriminantSome(), "0") +
		mirInsertValuePtrLine(fullReg, mirOptionPtrAggregateType(), stepReg, payloadPtr, "1")
}
func mirResultOkI64BuildLine(stepReg, fullReg, payload string) string {
	return mirInsertValueI64Line(stepReg, mirOptionAggregateType(), mirAggregateUndef(), mirDiscriminantOk(), "0") +
		mirInsertValueI64Line(fullReg, mirOptionAggregateType(), stepReg, payload, "1")
}
func mirResultOkPtrBuildLine(stepReg, fullReg, payloadPtr string) string {
	return mirInsertValueI64Line(stepReg, mirOptionPtrAggregateType(), mirAggregateUndef(), mirDiscriminantOk(), "0") +
		mirInsertValuePtrLine(fullReg, mirOptionPtrAggregateType(), stepReg, payloadPtr, "1")
}
func mirResultErrPtrBuildLine(stepReg, fullReg, errPtr string) string {
	return mirInsertValueI64Line(stepReg, mirOptionPtrAggregateType(), mirAggregateUndef(), mirDiscriminantErr(), "0") +
		mirInsertValuePtrLine(fullReg, mirOptionPtrAggregateType(), stepReg, errPtr, "1")
}

// §14 LLVM-text Option / Result destructure helpers.

// Osty: mirOptionUnwrapDestructureLine / mirOptionPtrUnwrapDestructureLine /
//       mirResultUnwrapDestructureLine
func mirOptionUnwrapDestructureLine(discReg, payloadReg, agg string) string {
	return mirOptionDiscProbeLine(discReg, agg) + mirOptionPayloadProbeLine(payloadReg, agg)
}
func mirOptionPtrUnwrapDestructureLine(discReg, payloadReg, agg string) string {
	return mirOptionPtrDiscProbeLine(discReg, agg) + mirOptionPtrPayloadProbeLine(payloadReg, agg)
}
func mirResultUnwrapDestructureLine(discReg, payloadReg, agg string) string {
	return mirResultDiscProbeLine(discReg, agg) + mirResultPayloadProbeLine(payloadReg, agg)
}

// §14 LLVM-text discriminant-comparison composite shapes.

// Osty: mirOptionIsSomeLine / mirOptionIsNoneLine / mirResultIsOkLine / mirResultIsErrLine
func mirOptionIsSomeLine(discReg, isSomeReg, agg string) string {
	return mirOptionDiscProbeLine(discReg, agg) +
		mirICmpI64EqLine(isSomeReg, discReg, mirDiscriminantSome())
}
func mirOptionIsNoneLine(discReg, isNoneReg, agg string) string {
	return mirOptionDiscProbeLine(discReg, agg) +
		mirICmpI64EqLine(isNoneReg, discReg, mirDiscriminantNone())
}
func mirResultIsOkLine(discReg, isOkReg, agg string) string {
	return mirResultDiscProbeLine(discReg, agg) +
		mirICmpI64EqLine(isOkReg, discReg, mirDiscriminantOk())
}
func mirResultIsErrLine(discReg, isErrReg, agg string) string {
	return mirResultDiscProbeLine(discReg, agg) +
		mirICmpI64EqLine(isErrReg, discReg, mirDiscriminantErr())
}

// §14 LLVM-text branch-on-discriminant composite shapes.

// Osty: mirBranchOnOptionDiscLine / mirBranchOnResultDiscLine
func mirBranchOnOptionDiscLine(discReg, cmpReg, agg, someLbl, noneLbl string) string {
	return mirOptionDiscProbeLine(discReg, agg) +
		mirICmpI64EqLine(cmpReg, discReg, mirDiscriminantSome()) +
		mirBrCondLine(cmpReg, someLbl, noneLbl)
}
func mirBranchOnResultDiscLine(discReg, cmpReg, agg, okLbl, errLbl string) string {
	return mirResultDiscProbeLine(discReg, agg) +
		mirICmpI64EqLine(cmpReg, discReg, mirDiscriminantOk()) +
		mirBrCondLine(cmpReg, okLbl, errLbl)
}

// §14 LLVM-text typed-string-pool global emit helpers.

// Osty: mirInternedStringPoolLine / mirInternedFormatPoolLine
func mirInternedStringPoolLine(sym, sizeDigits, encoded string) string {
	return mirGlobalStringPoolDeclLine(sym, sizeDigits, encoded)
}
func mirInternedFormatPoolLine(sym, sizeDigits, encoded string) string {
	return mirGlobalStringPoolDeclLine(sym, sizeDigits, encoded)
}

// §14 LLVM-text typed-cast call-arg shape composers.

// Osty: mirArgPtrCastFromI64Line / mirArgI64CastFromPtrLine
func mirArgPtrCastFromI64Line(castReg, val string) string {
	return mirCastI64ToPtrLine(castReg, val)
}
func mirArgI64CastFromPtrLine(castReg, val string) string {
	return mirCastPtrToI64Line(castReg, val)
}

// §14 LLVM-text Range / Iter shape composite helpers.

// Osty: mirForRangePreludeLine / mirForRangeHeadLine
func mirForRangePreludeLine(slotReg, start, headLbl string) string {
	return mirAllocaInitI64Line(slotReg, start) + mirBrLabelLine(headLbl)
}
func mirForRangeHeadLine(headLbl, iReg, slot, cmpReg, bound, bodyLbl, exitLbl string) string {
	return mirBlockHeaderLine(headLbl) +
		mirLoadI64Line(iReg, slot) +
		mirICmpI64SltLine(cmpReg, iReg, bound) +
		mirBrCondLine(cmpReg, bodyLbl, exitLbl)
}

// §14 LLVM-text Match-arm head-body shape helpers.

// Osty: mirMatchArmHeadLine / mirMatchArmBodyLine
func mirMatchArmHeadLine(idxDigits string) string {
	return mirLabelMatchArmPrefix() + "." + idxDigits + ":\n"
}
func mirMatchArmBodyLine(exitLbl string) string {
	return mirBrLabelLine(exitLbl)
}

// §14 LLVM-text bounds-check composite shapes.

// Osty: mirArrayBoundsCheckLine
func mirArrayBoundsCheckLine(nonNegReg, ltLenReg, andReg, idx, lenReg, okLbl, oobLbl string) string {
	return mirICmpI64SgeLine(nonNegReg, idx, "0") +
		mirICmpI64SltLine(ltLenReg, idx, lenReg) +
		mirAndI1Line(andReg, nonNegReg, ltLenReg) +
		mirBrCondLine(andReg, okLbl, oobLbl)
}

// §14 LLVM-text vector-list snapshot 3-line composite.

// Osty: mirVectorListSnapshot3Line
func mirVectorListSnapshot3Line(dataReg, dataSym, lenReg, listReg, scopeRef, headLbl string) string {
	return mirVectorListSnapshot2Line(dataReg, dataSym, lenReg, listReg, scopeRef) +
		mirBrLabelLine(headLbl)
}

// §14 LLVM-text typed-numerical-cast composites.

// Osty: mirCastFPToSIWithRTZLine / mirCastSIToFPDefaultLine
func mirCastFPToSIWithRTZLine(reg, fromTy, val, toTy string) string {
	return mirFPToSILine(reg, fromTy, val, toTy)
}
func mirCastSIToFPDefaultLine(reg, fromTy, val, toTy string) string {
	return mirSIToFPLine(reg, fromTy, val, toTy)
}

// §14 Generic memcpy composite — alloca + memcpy 2-line.

// Osty: mirAllocaThenMemcpyLine
func mirAllocaThenMemcpyLine(slotReg, ty, src, sizeBytesDigits string) string {
	return mirAllocaSingleLine(slotReg, ty) +
		mirCallVoidLLVMMemcpyLine(slotReg, src, sizeBytesDigits, "false")
}

// §14 LLVM-text typed-aggregate field-extract composite shapes.

// Osty: mirExtractValueI64FieldLine
func mirExtractValueI64FieldLine(reg, aggTy, agg, idx string) string {
	return mirExtractValueI64Line(reg, aggTy, agg, idx)
}

// §14 LLVM-text canonical zero-aggregate emit helpers.

// Osty: mirEmitOptionNoneI64Line / mirEmitOptionNonePtrLine
func mirEmitOptionNoneI64Line(reg string) string {
	return mirInsertValueI64Line(reg, mirOptionAggregateType(), mirAggregateUndef(), mirDiscriminantNone(), "0")
}
func mirEmitOptionNonePtrLine(reg string) string {
	return mirInsertValueI64Line(reg, mirOptionPtrAggregateType(), mirAggregateUndef(), mirDiscriminantNone(), "0")
}

// §14 Module-globals helpers.

// Osty: mirInternedI64GlobalLine / mirInternedPtrGlobalLine /
//       mirMutableI64GlobalLine / mirMutablePtrGlobalLine
func mirInternedI64GlobalLine(sym, val string) string {
	return mirGlobalConstantI64DeclLine(sym, val)
}
func mirInternedPtrGlobalLine(sym, val string) string {
	return mirGlobalConstantPtrDeclLine(sym, val)
}
func mirMutableI64GlobalLine(sym, init string) string {
	return mirGlobalMutableI64DeclLine(sym, init)
}
func mirMutablePtrGlobalLine(sym, init string) string {
	return mirGlobalMutablePtrDeclLine(sym, init)
}

// §14 LLVM-text typed-call-shape with no-args + result-store
// composite helpers.

// Osty: mirCallI64NoArgsAndStoreLine / mirCallI1NoArgsAndStoreLine /
//       mirCallPtrNoArgsAndStoreLine
func mirCallI64NoArgsAndStoreLine(reg, sym, slot string) string {
	return mirCallI64NoArgsLine(reg, sym) +
		"  " + mirInstrStore() + " i64 " + reg + ", ptr " + slot + "\n"
}
func mirCallI1NoArgsAndStoreLine(reg, sym, slot string) string {
	return mirCallI1NoArgsLine(reg, sym) +
		"  " + mirInstrStore() + " i1 " + reg + ", ptr " + slot + "\n"
}
func mirCallPtrNoArgsAndStoreLine(reg, sym, slot string) string {
	return mirCallPtrNoArgsLine(reg, sym) +
		"  " + mirInstrStore() + " ptr " + reg + ", ptr " + slot + "\n"
}

// §15 LLVM-text load-then-call composite helpers.

// Osty: mirLoadPtrThenCall{Void,Value,I64,I1}Line
func mirLoadPtrThenCallVoidLine(reg, slot, sym string) string {
	return mirLoadPtrLine(reg, slot) + mirCallVoidPtrLine(sym, reg)
}
func mirLoadPtrThenCallValueLine(loadReg, slot, resultReg, sym string) string {
	return mirLoadPtrLine(loadReg, slot) + mirCallValuePtrFromPtrLine(resultReg, sym, loadReg)
}
func mirLoadPtrThenCallI64Line(loadReg, slot, resultReg, sym string) string {
	return mirLoadPtrLine(loadReg, slot) + mirCallValueI64FromPtrLine(resultReg, sym, loadReg)
}
func mirLoadPtrThenCallI1Line(loadReg, slot, resultReg, sym string) string {
	return mirLoadPtrLine(loadReg, slot) + mirCallValueI1FromPtrLine(resultReg, sym, loadReg)
}

// §15 LLVM-text predicate-then-branch composite helpers.

// Osty: mirICmp{Eq,Ne,Slt,Sgt,Sle,Sge}I64ThenBranchLine / mirICmpEqPtrThenBranchLine /
//       mirICmpNullPtrThenBranchLine
func mirICmpEqI64ThenBranchLine(cmpReg, a, b, thenLbl, elseLbl string) string {
	return mirICmpI64EqLine(cmpReg, a, b) + mirBrCondLine(cmpReg, thenLbl, elseLbl)
}
func mirICmpNeI64ThenBranchLine(cmpReg, a, b, thenLbl, elseLbl string) string {
	return mirICmpI64NeLine(cmpReg, a, b) + mirBrCondLine(cmpReg, thenLbl, elseLbl)
}
func mirICmpSltI64ThenBranchLine(cmpReg, a, b, thenLbl, elseLbl string) string {
	return mirICmpI64SltLine(cmpReg, a, b) + mirBrCondLine(cmpReg, thenLbl, elseLbl)
}
func mirICmpSgtI64ThenBranchLine(cmpReg, a, b, thenLbl, elseLbl string) string {
	return mirICmpI64SgtLine(cmpReg, a, b) + mirBrCondLine(cmpReg, thenLbl, elseLbl)
}
func mirICmpSleI64ThenBranchLine(cmpReg, a, b, thenLbl, elseLbl string) string {
	return mirICmpI64SleLine(cmpReg, a, b) + mirBrCondLine(cmpReg, thenLbl, elseLbl)
}
func mirICmpSgeI64ThenBranchLine(cmpReg, a, b, thenLbl, elseLbl string) string {
	return mirICmpI64SgeLine(cmpReg, a, b) + mirBrCondLine(cmpReg, thenLbl, elseLbl)
}
func mirICmpEqPtrThenBranchLine(cmpReg, a, b, thenLbl, elseLbl string) string {
	return mirICmpPtrEqLine(cmpReg, a, b) + mirBrCondLine(cmpReg, thenLbl, elseLbl)
}
func mirICmpNullPtrThenBranchLine(cmpReg, a, thenLbl, elseLbl string) string {
	return mirICmpNullPtrLine(cmpReg, a) + mirBrCondLine(cmpReg, thenLbl, elseLbl)
}

// §15 LLVM-text typed-runtime-call sequencing composites.

// Osty: mirRuntimeProbeAndStoreLine
func mirRuntimeProbeAndStoreLine(reg, retTy, sym, args, slot string) string {
	return "  " + reg + " = " + mirInstrCall() + " " + retTy + " @" + sym + "(" + args + ")\n" +
		"  " + mirInstrStore() + " " + retTy + " " + reg + ", ptr " + slot + "\n"
}

// §15 LLVM-text typed-load-then-cmp composite helpers.

// Osty: mirLoadI64ThenCmp{Eq,Ne,Zero}Line / mirLoadPtrThenCmpNullLine
func mirLoadI64ThenCmpEqLine(loadReg, slot, cmpReg, b string) string {
	return mirLoadI64Line(loadReg, slot) + mirICmpI64EqLine(cmpReg, loadReg, b)
}
func mirLoadI64ThenCmpNeLine(loadReg, slot, cmpReg, b string) string {
	return mirLoadI64Line(loadReg, slot) + mirICmpI64NeLine(cmpReg, loadReg, b)
}
func mirLoadI64ThenCmpZeroLine(loadReg, slot, cmpReg string) string {
	return mirLoadI64Line(loadReg, slot) + mirICmpZeroI64Line(cmpReg, loadReg)
}
func mirLoadPtrThenCmpNullLine(loadReg, slot, cmpReg string) string {
	return mirLoadPtrLine(loadReg, slot) + mirICmpNullPtrLine(cmpReg, loadReg)
}

// §15 LLVM-text typed-load-then-store composite helpers.

// Osty: mirLoadI1ThenStoreLine / mirLoadDoubleThenStoreLine
func mirLoadI1ThenStoreLine(loadReg, src, dst string) string {
	return mirLoadStoreI1Line(loadReg, src, dst)
}
func mirLoadDoubleThenStoreLine(loadReg, src, dst string) string {
	return mirLoadStoreDoubleLine(loadReg, src, dst)
}

// §15 LLVM-text typed-runtime-call composite shapes (no result).

// Osty: mirCallVoidWithI64ResultLine
func mirCallVoidWithI64ResultLine(reg, sym, slot string) string {
	return mirCallI64NoArgsAndStoreLine(reg, sym, slot)
}

// §15 Common emit-pass shape composers — return-constant 1-line shapes.

// Osty: mirReturnConstant{I64,Ptr,I1,Double}Line / mirReturn{ZeroI64,NullPtr,FalseI1,TrueI1,ZeroDouble}Line
func mirReturnConstantI64Line(val string) string    { return mirRetTypedLine(mirTypeI64(), val) }
func mirReturnConstantPtrLine(val string) string    { return mirRetTypedLine(mirTypePtr(), val) }
func mirReturnConstantI1Line(val string) string     { return mirRetTypedLine(mirTypeI1(), val) }
func mirReturnConstantDoubleLine(val string) string { return mirRetTypedLine(mirTypeDouble(), val) }
func mirReturnZeroI64Line() string                  { return mirReturnConstantI64Line("0") }
func mirReturnNullPtrLine() string                  { return mirReturnConstantPtrLine("null") }
func mirReturnFalseI1Line() string                  { return mirReturnConstantI1Line("0") }
func mirReturnTrueI1Line() string                   { return mirReturnConstantI1Line("1") }
func mirReturnZeroDoubleLine() string               { return mirReturnConstantDoubleLine("0.0") }

// §15 LLVM-text typed call-arg list compositions (siblings).

// Osty: mirArgListPtrPtr / mirArgListPtrI64Slot / mirArgListThreePtrSlot
func mirArgListPtrPtr(a, b string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotPtr(b)
}
func mirArgListPtrI64Slot(a, b string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotI64(b)
}
func mirArgListThreePtrSlot(a, b, c string) string {
	return mirArgSlotPtr(a) + ", " + mirArgSlotPtr(b) + ", " + mirArgSlotPtr(c)
}

// §15 LLVM-text typed-i64-add-with-immediate composites.

// Osty: mirAddI64ImmediateThenStoreLine / mirSubI64ImmediateThenStoreLine
func mirAddI64ImmediateThenStoreLine(addReg, base, imm, slot string) string {
	return mirAddI64ImmediateLine(addReg, base, imm) +
		"  " + mirInstrStore() + " i64 " + addReg + ", ptr " + slot + "\n"
}
func mirSubI64ImmediateThenStoreLine(subReg, base, imm, slot string) string {
	return mirSubI64ImmediateLine(subReg, base, imm) +
		"  " + mirInstrStore() + " i64 " + subReg + ", ptr " + slot + "\n"
}

// §15 Block-builder composites — for typical 3-block "if-then-else"
// pattern.

// Osty: mirIfThenElseSkeletonLine
func mirIfThenElseSkeletonLine(cmpReg, cond, thenLbl, elseLbl string) string {
	return mirICmpI1NeLine(cmpReg, cond, "0") +
		mirBrCondLine(cmpReg, thenLbl, elseLbl) +
		mirBlockHeaderLine(thenLbl)
}

// §15 LLVM-text typed-aggregate-store composite helpers.

// Osty: mirStoreOptionAggregateLine / mirStoreOptionPtrAggregateLine /
//       mirStoreResultAggregateLine / mirStoreResultPtrAggregateLine
func mirStoreOptionAggregateLine(agg, slot string) string {
	return "  " + mirInstrStore() + " " + mirOptionAggregateType() + " " + agg + ", ptr " + slot + "\n"
}
func mirStoreOptionPtrAggregateLine(agg, slot string) string {
	return "  " + mirInstrStore() + " " + mirOptionPtrAggregateType() + " " + agg + ", ptr " + slot + "\n"
}
func mirStoreResultAggregateLine(agg, slot string) string {
	return mirStoreOptionAggregateLine(agg, slot)
}
func mirStoreResultPtrAggregateLine(agg, slot string) string {
	return mirStoreOptionPtrAggregateLine(agg, slot)
}

// §15 LLVM-text typed-aggregate-load composite helpers.

// Osty: mirLoadOptionAggregateLine / mirLoadOptionPtrAggregateLine /
//       mirLoadResultAggregateLine / mirLoadResultPtrAggregateLine
func mirLoadOptionAggregateLine(reg, slot string) string {
	return "  " + reg + " = " + mirInstrLoad() + " " + mirOptionAggregateType() + ", ptr " + slot + "\n"
}
func mirLoadOptionPtrAggregateLine(reg, slot string) string {
	return "  " + reg + " = " + mirInstrLoad() + " " + mirOptionPtrAggregateType() + ", ptr " + slot + "\n"
}
func mirLoadResultAggregateLine(reg, slot string) string {
	return mirLoadOptionAggregateLine(reg, slot)
}
func mirLoadResultPtrAggregateLine(reg, slot string) string {
	return mirLoadOptionPtrAggregateLine(reg, slot)
}

// §16 LLVM-text intrinsic-builder helpers (semantic aliases).

// Osty: mirCall{Sqrt,FAbs,Sin,Cos,Tan,Log,Log2,Log10,Exp,Exp2}DoubleLine
func mirCallSqrtDoubleLine(reg, x string) string  { return mirCallValueLLVMSqrtF64Line(reg, x) }
func mirCallFAbsDoubleLine(reg, x string) string  { return mirCallValueLLVMFAbsF64Line(reg, x) }
func mirCallSinDoubleLine(reg, x string) string   { return mirCallValueLLVMSinF64Line(reg, x) }
func mirCallCosDoubleLine(reg, x string) string   { return mirCallValueLLVMCosF64Line(reg, x) }
func mirCallTanDoubleLine(reg, x string) string   { return mirCallValueLLVMTanF64Line(reg, x) }
func mirCallLogDoubleLine(reg, x string) string   { return mirCallValueLLVMLogF64Line(reg, x) }
func mirCallLog2DoubleLine(reg, x string) string  { return mirCallValueLLVMLog2F64Line(reg, x) }
func mirCallLog10DoubleLine(reg, x string) string { return mirCallValueLLVMLog10F64Line(reg, x) }
func mirCallExpDoubleLine(reg, x string) string   { return mirCallValueLLVMExpF64Line(reg, x) }
func mirCallExp2DoubleLine(reg, x string) string  { return mirCallValueLLVMExp2F64Line(reg, x) }

// Osty: mirCallPowDoubleLine / MinNumDoubleLine / MaxNumDoubleLine
func mirCallPowDoubleLine(reg, base, exp string) string {
	return mirCallValueLLVMPowF64Line(reg, base, exp)
}
func mirCallMinNumDoubleLine(reg, a, b string) string {
	return mirCallValueLLVMMinNumF64Line(reg, a, b)
}
func mirCallMaxNumDoubleLine(reg, a, b string) string {
	return mirCallValueLLVMMaxNumF64Line(reg, a, b)
}

// §16 LLVM-text bit-manipulation typed-call composites.

// Osty: mirCall{Ctlz,Cttz,Ctpop,BSwapI{64,32,16},BitReverse}I64Line
func mirCallCtlzI64Line(reg, x string) string       { return mirCallValueLLVMCtlzI64Line(reg, x) }
func mirCallCttzI64Line(reg, x string) string       { return mirCallValueLLVMCttzI64Line(reg, x) }
func mirCallCtpopI64Line(reg, x string) string      { return mirCallValueLLVMCtpopI64Line(reg, x) }
func mirCallBSwapI64Line(reg, x string) string      { return mirCallValueLLVMBSwapI64Line(reg, x) }
func mirCallBSwapI32Line(reg, x string) string      { return mirCallValueLLVMBSwapI32Line(reg, x) }
func mirCallBSwapI16Line(reg, x string) string      { return mirCallValueLLVMBSwapI16Line(reg, x) }
func mirCallBitReverseI64Line(reg, x string) string { return mirCallValueLLVMBitReverseI64Line(reg, x) }

// §16 LLVM-text typed-runtime-callable lifetime intrinsics.

// Osty: mirCallLifetime{Start,End}Line / mirCallAssumeLine / mirCallExpectI1Line
func mirCallLifetimeStartLine(sizeBytes, slot string) string {
	return mirCallVoidLLVMLifetimeStartLine(sizeBytes, slot)
}
func mirCallLifetimeEndLine(sizeBytes, slot string) string {
	return mirCallVoidLLVMLifetimeEndLine(sizeBytes, slot)
}
func mirCallAssumeLine(cond string) string {
	return mirCallVoidLLVMAssumeLine(cond)
}
func mirCallExpectI1Line(reg, cond, expected string) string {
	return mirCallValueLLVMExpectI1Line(reg, cond, expected)
}

// §16 LLVM-text mem-intrinsic typed call shapes.

// Osty: mirCallMemcpy{Volatile,NonVolatile}Line / MemmoveNonVolatileLine / MemsetZeroLine
func mirCallMemcpyVolatileLine(dst, src, sizeBytes string) string {
	return mirCallVoidLLVMMemcpyLine(dst, src, sizeBytes, "true")
}
func mirCallMemcpyNonVolatileLine(dst, src, sizeBytes string) string {
	return mirCallVoidLLVMMemcpyLine(dst, src, sizeBytes, "false")
}
func mirCallMemmoveNonVolatileLine(dst, src, sizeBytes string) string {
	return mirCallVoidLLVMMemmoveLine(dst, src, sizeBytes, "false")
}
func mirCallMemsetZeroLine(dst, sizeBytes string) string {
	return mirCallVoidLLVMMemsetLine(dst, "0", sizeBytes, "false")
}

// §16 LLVM-text typed-runtime-callable testing helpers.

// Osty: mirCallTest{Abort,ContextEnter,ContextExit,ExpectOk,ExpectError}Line
func mirCallTestAbortLine(messagePtr string) string {
	return mirCallVoidTestingAbortLine(messagePtr)
}
func mirCallTestContextEnterLine(nameReg string) string {
	return mirCallVoidTestingContextEnterLine(nameReg)
}
func mirCallTestContextExitLine() string {
	return mirCallVoidTestingContextExitLine()
}
func mirCallTestExpectOkLine(reg, resultReg string) string {
	return mirCallValueTestingExpectOkLine(reg, resultReg)
}
func mirCallTestExpectErrorLine(reg, resultReg string) string {
	return mirCallValueTestingExpectErrorLine(reg, resultReg)
}

// §16 LLVM-text typed runtime-callable bench helpers.

// Osty: mirCallBench{NowNanos,TargetNs}Line
func mirCallBenchNowNanosLine(reg string) string {
	return mirCallValueBenchNowNanosLine(reg)
}
func mirCallBenchTargetNsLine(reg string) string {
	return mirCallValueBenchTargetNsLine(reg)
}

// §16 LLVM-text typed-runtime-callable GC helpers.

// Osty: mirCallGC{Alloc,Safepoint,Barrier,AllocatedBytes}Line
func mirCallGCAllocLine(reg, sizeBytes, kind string) string {
	return mirCallValueGCAllocLine(reg, sizeBytes, kind)
}
func mirCallGCSafepointLine(slotsPtr, slotCount string) string {
	return mirCallVoidGCSafepointLine(slotsPtr, slotCount)
}
func mirCallGCBarrierLine(targetPtr, valuePtr string) string {
	return mirCallVoidGCBarrierLine(targetPtr, valuePtr)
}
func mirCallGCAllocatedBytesLine(reg string) string {
	return mirCallValueGCAllocatedBytesLine(reg)
}

// §16 LLVM-text typed runtime-callable string-debug helpers.

// Osty: mirCallDiffLinesLine
func mirCallDiffLinesLine(reg, expectedReg, actualReg string) string {
	return mirCallValueStringDiffLinesLine(reg, expectedReg, actualReg)
}

// §16 LLVM-text typed-call-and-no-store composites.

// Osty: mirCallVoidWithI1ResultDiscardLine
func mirCallVoidWithI1ResultDiscardLine(reg, sym, args string) string {
	return "  " + reg + " = " + mirInstrCall() + " i1 @" + sym + "(" + args + ")\n"
}

// §16 LLVM-text option/result spilling helpers.

// Osty: mirSpill{Option,OptionPtr,Result,ResultPtr}AggregateLine
func mirSpillOptionAggregateLine(slotReg, agg string) string {
	return mirAllocaSingleLine(slotReg, mirOptionAggregateType()) +
		mirStoreOptionAggregateLine(agg, slotReg)
}
func mirSpillOptionPtrAggregateLine(slotReg, agg string) string {
	return mirAllocaSingleLine(slotReg, mirOptionPtrAggregateType()) +
		mirStoreOptionPtrAggregateLine(agg, slotReg)
}
func mirSpillResultAggregateLine(slotReg, agg string) string {
	return mirSpillOptionAggregateLine(slotReg, agg)
}
func mirSpillResultPtrAggregateLine(slotReg, agg string) string {
	return mirSpillOptionPtrAggregateLine(slotReg, agg)
}

// §16 LLVM-text typed-arg list 4/5/6-elem composers (aliases).

// Osty: mirArgListFour/Five/SixMixed
func mirArgListFourMixed(a, b, c, d string) string {
	return mirJoinCommaFour(a, b, c, d)
}
func mirArgListFiveMixed(a, b, c, d, e string) string {
	return mirJoinCommaFive(a, b, c, d, e)
}
func mirArgListSixMixed(a, b, c, d, e, f string) string {
	return mirJoinCommaSix(a, b, c, d, e, f)
}

// §16 LLVM-text emit-pass header / footer helpers.

// Osty: mirModulePreambleLine / mirModuleEpilogueLine
func mirModulePreambleLine(sourcePath, triple, layout string) string {
	return mirModuleHeaderSourceFilename(sourcePath) +
		mirModuleHeaderTargetTriple(triple) +
		mirModuleHeaderDataLayout(layout)
}
func mirModuleEpilogueLine(version string) string {
	return mirIdentList(mirCompilerInfoLine(version))
}

// §16 LLVM-text typed-cast-then-call composite shapes.

// Osty: mirCastI64ToPtrThenCallLine / mirCastPtrToI64ThenCallLine
func mirCastI64ToPtrThenCallLine(castReg, val, resultReg, sym string) string {
	return mirCastI64ToPtrLine(castReg, val) +
		mirCallValuePtrFromPtrLine(resultReg, sym, castReg)
}
func mirCastPtrToI64ThenCallLine(castReg, val, resultReg, sym, args string) string {
	return mirCastPtrToI64Line(castReg, val) +
		"  " + resultReg + " = " + mirInstrCall() + " i64 @" + sym + "(" + args + ")\n"
}

// §16 LLVM-text typed-zext-then-call composite shapes.

// Osty: mirZExtI{8,1}ToI64ThenCallLine
func mirZExtI8ToI64ThenCallLine(zextReg, val, resultReg, sym string) string {
	return mirZExtI8ToI64Line(zextReg, val) +
		mirCallValueI64FromPtrLine(resultReg, sym, zextReg)
}
func mirZExtI1ToI64ThenCallLine(zextReg, val, resultReg, sym string) string {
	return mirZExtI1ToI64Line(zextReg, val) +
		mirCallValueI64FromPtrLine(resultReg, sym, zextReg)
}

// §16 LLVM-text typed-trunc-then-store composite shapes.

// Osty: mirTruncI64To{I8,I1,I32}ThenStoreLine
func mirTruncI64ToI8ThenStoreLine(truncReg, val, slot string) string {
	return mirTruncI64ToI8Line(truncReg, val) + mirStoreI8Line(truncReg, slot)
}
func mirTruncI64ToI1ThenStoreLine(truncReg, val, slot string) string {
	return mirTruncI64ToI1Line(truncReg, val) +
		"  " + mirInstrStore() + " i1 " + truncReg + ", ptr " + slot + "\n"
}
func mirTruncI64ToI32ThenStoreLine(truncReg, val, slot string) string {
	return mirTruncI64ToI32Line(truncReg, val) + mirStoreI32Line(truncReg, slot)
}

// §17 LLVM-text typed list / map / set runtime call aliases.

// Osty: mirCallListLengthLine / MapLengthLine / SetLengthLine /
//       StringLengthLine / BytesLengthLine
func mirCallListLengthLine(reg, listReg string) string {
	return mirCallListLenLine(reg, listReg)
}
func mirCallMapLengthLine(reg, mapReg string) string {
	return mirCallMapLenLine(reg, mapReg)
}
func mirCallSetLengthLine(reg, setReg string) string {
	return mirCallSetLenLine(reg, setReg)
}
func mirCallStringLengthLine(reg, strReg string) string {
	return mirCallValueI64FromPtrLine(reg, mirRtStringLenSymbol(), strReg)
}
func mirCallBytesLengthLine(reg, bytesReg string) string {
	return mirCallBytesLenLine(reg, bytesReg)
}

// §17 LLVM-text typed iterator-call shapes.

// Osty: mirCallList{Reverse,Reversed,Clear,PopDiscard,IsEmptyTyped}Line /
//       mirCallMapClearLine / mirCallSetClearLine
func mirCallListReverseLine(listReg string) string {
	return mirCallVoidPtrLine(mirRtListReverseSymbol(), listReg)
}
func mirCallListReversedLine(reg, listReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtListReversedSymbol(), listReg)
}
func mirCallListClearLine(listReg string) string {
	return mirCallVoidPtrLine(mirRtListClearSymbol(), listReg)
}
func mirCallMapClearLine(mapReg string) string {
	return mirCallVoidPtrLine(mirRtMapClearSymbol(), mapReg)
}
func mirCallSetClearLine(setReg string) string {
	return mirCallVoidPtrLine(mirRtSetClearSymbol(), setReg)
}
func mirCallListPopDiscardLine(listReg string) string {
	return mirCallVoidPtrLine(mirRtListPopDiscardSymbol(), listReg)
}
func mirCallListIsEmptyTypedLine(reg, listReg string) string {
	return mirCallValueI1FromPtrLine(reg, mirRtListIsEmptySymbol(), listReg)
}

// §17 LLVM-text typed-runtime-callable container-conversion helpers.

// Osty: mirCallSetToListLine / MapValuesLine / MapEntriesLine
func mirCallSetToListLine(reg, setReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtSetToListSymbol(), setReg)
}
func mirCallMapValuesLine(reg, mapReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtMapValuesSymbol(), mapReg)
}
func mirCallMapEntriesLine(reg, mapReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtMapEntriesSymbol(), mapReg)
}

// §17 LLVM-text typed-runtime-callable channel-state helpers.

// Osty: mirCallChannel{Close,IsClosed,Len,Cap}Line
func mirCallChannelCloseLine(chanReg string) string {
	return mirCallVoidPtrLine(mirRtChanCloseSymbol(), chanReg)
}
func mirCallChannelIsClosedLine(reg, chanReg string) string {
	return mirCallValueI1FromPtrLine(reg, mirRtChanIsClosedSymbol(), chanReg)
}
func mirCallChannelLenLine(reg, chanReg string) string {
	return mirCallValueI64FromPtrLine(reg, mirRtChanLenSymbol(), chanReg)
}
func mirCallChannelCapLine(reg, chanReg string) string {
	return mirCallValueI64FromPtrLine(reg, mirRtChanCapSymbol(), chanReg)
}

// §17 LLVM-text typed-runtime-callable cancel helpers.

// Osty: mirCallCancel{Check,IsCancelled,Cancel}Line
func mirCallCancelCheckLine(reg string) string {
	return mirCallI1NoArgsLine(reg, mirRtCancelCheckCancelledSymbol())
}
func mirCallCancelIsCancelledLine(reg string) string {
	return mirCallI1NoArgsLine(reg, mirRtCancelIsCancelledSymbol())
}
func mirCallCancelCancelLine() string {
	return mirCallVoidNoArgsLine(mirRtCancelCancelSymbol())
}

// §17 LLVM-text typed-runtime-callable thread-state helpers.

// Osty: mirCallThread{Yield,Sleep}Line
func mirCallThreadYieldLine() string {
	return mirCallVoidNoArgsLine(mirRtThreadYieldSymbol())
}
func mirCallThreadSleepLine(nsReg string) string {
	return mirCallVoidFromI64Line(mirRtThreadSleepSymbol(), nsReg)
}

// §17 LLVM-text typed-runtime-callable string operations.

// Osty: mirCallStringConcatTwoLine / Hash / IsEmpty / ToUpper / ToLower / Trim / Repeat
func mirCallStringConcatTwoLine(reg, leftReg, rightReg string) string {
	return mirCallPtrFromTwoPtrLine(reg, mirRtStringConcatSymbol(), leftReg, rightReg)
}
func mirCallStringHashLine(reg, strReg string) string {
	return mirCallValueI64FromPtrLine(reg, mirRtStringHashSymbol(), strReg)
}
func mirCallStringIsEmptyLine(reg, strReg string) string {
	return mirCallValueI1FromPtrLine(reg, mirRtStringIsEmptySymbol(), strReg)
}
func mirCallStringToUpperLine(reg, strReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtStringToUpperSymbol(), strReg)
}
func mirCallStringToLowerLine(reg, strReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtStringToLowerSymbol(), strReg)
}
func mirCallStringTrimLine(reg, strReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtStringTrimSymbol(), strReg)
}
func mirCallStringRepeatLine(reg, strReg, nReg string) string {
	return mirCallPtrFromPtrI64Line(reg, mirRtStringRepeatSymbol(), strReg, nReg)
}

// §17 LLVM-text typed-runtime-callable bytes operations.

// Osty: mirCallBytesIsEmptyLine / GetTypedLine / ContainsLine / StartsWithLine / EndsWithLine
func mirCallBytesIsEmptyLine(reg, bytesReg string) string {
	return mirCallValueI1FromPtrLine(reg, mirRtBytesIsEmptySymbol(), bytesReg)
}
func mirCallBytesGetTypedLine(reg, bytesReg, idxReg string) string {
	return "  " + reg + " = " + mirInstrCall() + " i8 @" + mirRtBytesGetSymbol() + "(ptr " + bytesReg + ", i64 " + idxReg + ")\n"
}
func mirCallBytesContainsLine(reg, bytesReg, needleReg string) string {
	return mirCallI1FromTwoPtrLine(reg, mirRtBytesContainsSymbol(), bytesReg, needleReg)
}
func mirCallBytesStartsWithLine(reg, bytesReg, prefixReg string) string {
	return mirCallI1FromTwoPtrLine(reg, mirRtBytesStartsWithSymbol(), bytesReg, prefixReg)
}
func mirCallBytesEndsWithLine(reg, bytesReg, suffixReg string) string {
	return mirCallI1FromTwoPtrLine(reg, mirRtBytesEndsWithSymbol(), bytesReg, suffixReg)
}

// §17 LLVM-text typed runtime-callable map / set operations.

// Osty: mirCallMap{Insert,Remove}Line / mirCallSet{Add,Remove}Line
func mirCallMapInsertLine(mapReg, keyArgs, valArgs string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirRtMapInsertSymbol() + "(ptr " + mapReg + ", " + keyArgs + ", " + valArgs + ")\n"
}
func mirCallMapRemoveLine(mapReg, keyArgs string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirRtMapRemoveSymbol() + "(ptr " + mapReg + ", " + keyArgs + ")\n"
}
func mirCallSetAddLine(setReg, elemArgs string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirRtSetAddSymbol() + "(ptr " + setReg + ", " + elemArgs + ")\n"
}
func mirCallSetRemoveLine(setReg, elemArgs string) string {
	return "  " + mirInstrCallVoid() + " void @" + mirRtSetRemoveSymbol() + "(ptr " + setReg + ", " + elemArgs + ")\n"
}

// §17 LLVM-text typed runtime-callable structured-concurrency.

// Osty: mirCallTaskGroup{New,Cancel,IsCancelled}Line / mirCallTaskHandleJoinLine /
//       mirCallTaskCollectAllLine / mirCallTaskRaceLine
func mirCallTaskGroupNewLine(reg string) string {
	return mirCallPtrNoArgsLine(reg, mirRtTaskGroupRootSymbol())
}
func mirCallTaskGroupCancelLine(groupReg string) string {
	return mirCallVoidPtrLine(mirRtTaskGroupCancelSymbol(), groupReg)
}
func mirCallTaskGroupIsCancelledLine(reg, groupReg string) string {
	return mirCallValueI1FromPtrLine(reg, mirRtTaskGroupIsCancelledSymbol(), groupReg)
}
func mirCallTaskHandleJoinLine(reg, handleReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtTaskHandleJoinSymbol(), handleReg)
}
func mirCallTaskCollectAllLine(reg, listReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtTaskCollectAllSymbol(), listReg)
}
func mirCallTaskRaceLine(reg, bodyReg string) string {
	return mirCallValuePtrFromPtrLine(reg, mirRtTaskRaceSymbol(), bodyReg)
}

// §17 LLVM-text typed runtime-callable panic / abort helpers.

// Osty: mirCall{Panic,Unreachable,Todo,Abort}Line / mirCall{OptionUnwrapNone,ResultUnwrapErr,ExpectFailed}Line
func mirCallPanicLine(messagePtr string) string {
	return mirCallVoidPtrLine(mirRtPanicSymbol(), messagePtr)
}
func mirCallUnreachableLine() string {
	return mirCallVoidNoArgsLine(mirRtUnreachableSymbol())
}
func mirCallTodoLine() string {
	return mirCallVoidNoArgsLine(mirRtTodoSymbol())
}
func mirCallAbortLine() string {
	return mirCallVoidNoArgsLine(mirRtAbortSymbol())
}
func mirCallOptionUnwrapNoneLine() string {
	return mirCallVoidNoArgsLine(mirRtOptionUnwrapNoneSymbol())
}
func mirCallResultUnwrapErrLine() string {
	return mirCallVoidNoArgsLine(mirRtResultUnwrapErrSymbol())
}
func mirCallExpectFailedLine() string {
	return mirCallVoidNoArgsLine(mirRtExpectFailedSymbol())
}

// §18 Phase-2 function ports — small pure helpers ported from
// internal/llvmgen/mir_generator.go.

// Osty: mirLocalDisplayNameFromText
func mirLocalDisplayNameFromText(name string) string {
	if name == "" {
		return "<temp>"
	}
	return name
}

// Osty: mirIntrinsicKindLabel
func mirIntrinsicKindLabel(kind int, kindFallback string) string {
	switch kind {
	case 1:
		return "print"
	case 2:
		return "println"
	case 3:
		return "eprint"
	case 4:
		return "eprintln"
	case 6:
		return "string_concat"
	}
	return kindFallback
}

// Osty: mirIntrinsicKindFallbackLabel
func mirIntrinsicKindFallbackLabel(kindDigits string) string {
	return "kind=" + kindDigits
}

// (mirChannelRecvSuffix / mirElemSizeBytes are not added — mirChanRecvSuffix /
// mirMapValueSizeBytes already serve the same role; aliases would shadow them.)

// §18 (cont'd) — built-in named-type predicates ported from
// is{List,Map,Set}PtrType in mir_generator.go.

// Osty: mirIsBuiltinNamedType
func mirIsBuiltinNamedType(name string, isBuiltin bool, expected string) bool {
	return name == expected && isBuiltin
}

// Osty: mirIsBuiltin{List,Map,Set}
func mirIsBuiltinList(name string, isBuiltin bool) bool {
	return mirIsBuiltinNamedType(name, isBuiltin, "List")
}
func mirIsBuiltinMap(name string, isBuiltin bool) bool {
	return mirIsBuiltinNamedType(name, isBuiltin, "Map")
}
func mirIsBuiltinSet(name string, isBuiltin bool) bool {
	return mirIsBuiltinNamedType(name, isBuiltin, "Set")
}

// §18 (cont'd) — typed-list runtime fast-path predicate ported from
// listUsesRawDataFastPath in runtime_ffi.go.

// Osty: mirListUsesRawDataFastPath
func mirListUsesRawDataFastPath(elemLLVM string) bool {
	return elemLLVM == "i64" || elemLLVM == "i1" || elemLLVM == "double"
}

// §18 (cont'd) — alloca-hoist scanner predicates ported from
// alloca_hoist.go.

// Osty: mirIsBrTerminatorLine
func mirIsBrTerminatorLine(line string) bool {
	return llvmStrings.HasPrefix(line, "  br ")
}

// Osty: mirIsAllocaLine
func mirIsAllocaLine(line string) bool {
	const prefix = "  %"
	if !llvmStrings.HasPrefix(line, prefix) {
		return false
	}
	rest := line[len(prefix):]
	eq := llvmStrings.Index(rest, " = ")
	if eq < 0 {
		return false
	}
	const separator = " = "
	tail := rest[eq+len(separator):]
	return llvmStrings.HasPrefix(tail, "alloca ") || tail == "alloca"
}

// §18 (cont'd) — primitive scrutinee / std.io output / list-primitive
// kind helpers ported from expr.go / stdlib_io_shim.go / stmt.go.

// Osty: mirIsPrimitiveLiteralMatchScrutineeType
func mirIsPrimitiveLiteralMatchScrutineeType(typ string) bool {
	return typ == "i64" || typ == "i32" || typ == "i8" || typ == "i1"
}

// Osty: mirIsStdIoOutputMethod
func mirIsStdIoOutputMethod(name string) bool {
	return name == "print" || name == "println" || name == "eprint" || name == "eprintln"
}

// Osty: mirListPrimitiveKindID
func mirListPrimitiveKindID(elemTyp string, elemIsString bool) int {
	switch elemTyp {
	case "i64":
		return 1
	case "double":
		return 2
	case "i1":
		return 3
	case "ptr":
		if elemIsString {
			return 4
		}
	}
	return 0
}
