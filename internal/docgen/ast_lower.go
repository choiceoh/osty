package docgen

import (
	"fmt"
	"math"
	"strings"

	astbridge "github.com/osty/osty/internal/docgen/astbridge"
)

func astLowerPublicFile(arena *AstArena, toks []astbridge.Token) astbridge.File {
	// Osty: /tmp/selfhost_merged.osty:17354:5
	uses := astbridge.EmptyDeclList()
	_ = uses
	// Osty: /tmp/selfhost_merged.osty:17355:5
	decls := astbridge.EmptyDeclList()
	_ = decls
	// Osty: /tmp/selfhost_merged.osty:17356:5
	stmts := astbridge.EmptyStmtList()
	_ = stmts
	// Osty: /tmp/selfhost_merged.osty:17357:5
	for _, idx := range arena.decls {
		// Osty: /tmp/selfhost_merged.osty:17358:9
		n := astArenaNodeAt(arena, idx)
		_ = n
		// Osty: /tmp/selfhost_merged.osty:17359:9
		if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNLet{})) && !(astbridge.TokenIsPub(astLowerTok(toks, func() int {
			var _p2396 int = n.start
			var _rhs2397 int = 1
			if _rhs2397 < 0 && _p2396 > math.MaxInt+_rhs2397 {
				panic("integer overflow")
			}
			if _rhs2397 > 0 && _p2396 < math.MinInt+_rhs2397 {
				panic("integer overflow")
			}
			return _p2396 - _rhs2397
		}()))) {
			// Osty: /tmp/selfhost_merged.osty:17360:13
			stmt := astLowerStmt(arena, toks, idx)
			_ = stmt
			// Osty: /tmp/selfhost_merged.osty:17361:13
			if !(astbridge.IsNilStmt(stmt)) {
				// Osty: /tmp/selfhost_merged.osty:17362:17
				func() struct{} { stmts = append(stmts, stmt); return struct{}{} }()
			}
		} else {
			// Osty: /tmp/selfhost_merged.osty:17365:13
			decl := astLowerDecl(arena, toks, idx)
			_ = decl
			// Osty: /tmp/selfhost_merged.osty:17366:13
			if !(astbridge.IsNilDecl(decl)) {
				// Osty: /tmp/selfhost_merged.osty:17367:17
				if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNUseDecl{})) {
					// Osty: /tmp/selfhost_merged.osty:17368:21
					func() struct{} { uses = append(uses, decl); return struct{}{} }()
				} else {
					// Osty: /tmp/selfhost_merged.osty:17370:21
					func() struct{} { decls = append(decls, decl); return struct{}{} }()
				}
			} else {
				// Osty: /tmp/selfhost_merged.osty:17373:17
				stmt := astLowerStmt(arena, toks, idx)
				_ = stmt
				// Osty: /tmp/selfhost_merged.osty:17374:17
				if !(astbridge.IsNilStmt(stmt)) {
					// Osty: /tmp/selfhost_merged.osty:17375:21
					func() struct{} { stmts = append(stmts, stmt); return struct{}{} }()
				}
			}
		}
	}
	return astbridge.FileNode(astLowerPos(toks, 0), astLowerEnd(toks, func() int {
		var _p2398 int = astLowerTokenCount(toks)
		var _rhs2399 int = 1
		if _rhs2399 < 0 && _p2398 > math.MaxInt+_rhs2399 {
			panic("integer overflow")
		}
		if _rhs2399 > 0 && _p2398 < math.MinInt+_rhs2399 {
			panic("integer overflow")
		}
		return _p2398 - _rhs2399
	}()), uses, decls, stmts)
}

// Osty: /tmp/selfhost_merged.osty:17383:1
func astLowerTokenCount(toks []astbridge.Token) int {
	// Osty: /tmp/selfhost_merged.osty:17384:5
	count := 0
	_ = count
	// Osty: /tmp/selfhost_merged.osty:17385:5
	for _, tok := range toks {
		// Osty: /tmp/selfhost_merged.osty:17386:9
		_ = tok
		// Osty: /tmp/selfhost_merged.osty:17387:9
		func() {
			var _cur2400 int = count
			var _rhs2401 int = 1
			if _rhs2401 > 0 && _cur2400 > math.MaxInt-_rhs2401 {
				panic("integer overflow")
			}
			if _rhs2401 < 0 && _cur2400 < math.MinInt-_rhs2401 {
				panic("integer overflow")
			}
			count = _cur2400 + _rhs2401
		}()
	}
	return count
}

// Osty: /tmp/selfhost_merged.osty:17392:1
func astLowerInterpolatedTokensToExpr(toks []astbridge.Token) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:17393:5
	count := astLowerTokenCount(toks)
	_ = count
	return astLowerInterpExpr(toks, 0, count)
}

// Osty: /tmp/selfhost_merged.osty:17397:1
func astLowerInterpExpr(toks []astbridge.Token, rawStart int, rawEnd int) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:17398:5
	start := astLowerInterpTrimStart(toks, rawStart, rawEnd)
	_ = start
	// Osty: /tmp/selfhost_merged.osty:17399:5
	end := astLowerInterpTrimEnd(toks, start, rawEnd)
	_ = end
	// Osty: /tmp/selfhost_merged.osty:17400:5
	if start >= end {
		// Osty: /tmp/selfhost_merged.osty:17401:9
		return astbridge.NilExpr()
	}
	// Osty: /tmp/selfhost_merged.osty:17403:5
	split := astLowerInterpSplitTopLevelBinary(toks, start, end)
	_ = split
	// Osty: /tmp/selfhost_merged.osty:17404:5
	if split > start {
		// Osty: /tmp/selfhost_merged.osty:17405:9
		left := astLowerInterpExpr(toks, start, split)
		_ = left
		// Osty: /tmp/selfhost_merged.osty:17406:9
		right := astLowerInterpExpr(toks, func() int {
			var _p2402 int = split
			var _rhs2403 int = 1
			if _rhs2403 > 0 && _p2402 > math.MaxInt-_rhs2403 {
				panic("integer overflow")
			}
			if _rhs2403 < 0 && _p2402 < math.MinInt-_rhs2403 {
				panic("integer overflow")
			}
			return _p2402 + _rhs2403
		}(), end)
		_ = right
		// Osty: /tmp/selfhost_merged.osty:17407:9
		return astbridge.BinaryExprNode(astLowerPos(toks, start), astLowerEnd(toks, end), astbridge.TokenKind(astLowerTok(toks, split)), left, right)
	}
	// Osty: /tmp/selfhost_merged.osty:17415:5
	first := astLowerTok(toks, start)
	_ = first
	// Osty: /tmp/selfhost_merged.osty:17416:5
	firstKind := astbridge.TokenKindString(first)
	_ = firstKind
	// Osty: /tmp/selfhost_merged.osty:17417:5
	if firstKind == "-" || firstKind == "!" || firstKind == "~" {
		// Osty: /tmp/selfhost_merged.osty:17418:9
		x := astLowerInterpExpr(toks, func() int {
			var _p2404 int = start
			var _rhs2405 int = 1
			if _rhs2405 > 0 && _p2404 > math.MaxInt-_rhs2405 {
				panic("integer overflow")
			}
			if _rhs2405 < 0 && _p2404 < math.MinInt-_rhs2405 {
				panic("integer overflow")
			}
			return _p2404 + _rhs2405
		}(), end)
		_ = x
		// Osty: /tmp/selfhost_merged.osty:17419:9
		return astbridge.UnaryExprNode(astLowerPos(toks, start), astLowerEnd(toks, end), astbridge.TokenKind(first), x)
	}
	// Osty: /tmp/selfhost_merged.osty:17421:5
	if firstKind == "(" && astLowerInterpFindClose(toks, start, end, "(", ")") == func() int {
		var _p2406 int = end
		var _rhs2407 int = 1
		if _rhs2407 < 0 && _p2406 > math.MaxInt+_rhs2407 {
			panic("integer overflow")
		}
		if _rhs2407 > 0 && _p2406 < math.MinInt+_rhs2407 {
			panic("integer overflow")
		}
		return _p2406 - _rhs2407
	}() {
		// Osty: /tmp/selfhost_merged.osty:17422:9
		return astbridge.ParenExprNode(astLowerPos(toks, start), astLowerEnd(toks, end), astLowerInterpExpr(toks, func() int {
			var _p2408 int = start
			var _rhs2409 int = 1
			if _rhs2409 > 0 && _p2408 > math.MaxInt-_rhs2409 {
				panic("integer overflow")
			}
			if _rhs2409 < 0 && _p2408 < math.MinInt-_rhs2409 {
				panic("integer overflow")
			}
			return _p2408 + _rhs2409
		}(), func() int {
			var _p2410 int = end
			var _rhs2411 int = 1
			if _rhs2411 < 0 && _p2410 > math.MaxInt+_rhs2411 {
				panic("integer overflow")
			}
			if _rhs2411 > 0 && _p2410 < math.MinInt+_rhs2411 {
				panic("integer overflow")
			}
			return _p2410 - _rhs2411
		}()))
	}
	// Osty: /tmp/selfhost_merged.osty:17424:5
	expr := astLowerInterpPrimary(toks, start, end)
	_ = expr
	// Osty: /tmp/selfhost_merged.osty:17425:5
	if astbridge.IsNilExpr(expr) {
		// Osty: /tmp/selfhost_merged.osty:17426:9
		return astbridge.IdentExpr(astLowerPos(toks, start), astLowerEnd(toks, end), "__interp")
	}
	// Osty: /tmp/selfhost_merged.osty:17428:5
	i := func() int {
		var _p2412 int = start
		var _rhs2413 int = 1
		if _rhs2413 > 0 && _p2412 > math.MaxInt-_rhs2413 {
			panic("integer overflow")
		}
		if _rhs2413 < 0 && _p2412 < math.MinInt-_rhs2413 {
			panic("integer overflow")
		}
		return _p2412 + _rhs2413
	}()
	_ = i
	// Osty: /tmp/selfhost_merged.osty:17429:5
	for i < end {
		// Osty: /tmp/selfhost_merged.osty:17430:9
		tok := astLowerTok(toks, i)
		_ = tok
		// Osty: /tmp/selfhost_merged.osty:17431:9
		kind := astbridge.TokenKindString(tok)
		_ = kind
		// Osty: /tmp/selfhost_merged.osty:17432:9
		if kind == "(" {
			// Osty: /tmp/selfhost_merged.osty:17433:13
			close := astLowerInterpFindClose(toks, i, end, "(", ")")
			_ = close
			// Osty: /tmp/selfhost_merged.osty:17434:13
			if close < 0 {
				// Osty: /tmp/selfhost_merged.osty:17435:17
				return astbridge.CallExprNode(astbridge.ExprPos(expr, astLowerPos(toks, start)), astLowerEnd(toks, end), expr, astLowerInterpArgs(toks, func() int {
					var _p2414 int = i
					var _rhs2415 int = 1
					if _rhs2415 > 0 && _p2414 > math.MaxInt-_rhs2415 {
						panic("integer overflow")
					}
					if _rhs2415 < 0 && _p2414 < math.MinInt-_rhs2415 {
						panic("integer overflow")
					}
					return _p2414 + _rhs2415
				}(), end))
			}
			// Osty: /tmp/selfhost_merged.osty:17437:13
			expr = astbridge.CallExprNode(astbridge.ExprPos(expr, astLowerPos(toks, start)), astLowerEnd(toks, func() int {
				var _p2416 int = close
				var _rhs2417 int = 1
				if _rhs2417 > 0 && _p2416 > math.MaxInt-_rhs2417 {
					panic("integer overflow")
				}
				if _rhs2417 < 0 && _p2416 < math.MinInt-_rhs2417 {
					panic("integer overflow")
				}
				return _p2416 + _rhs2417
			}()), expr, astLowerInterpArgs(toks, func() int {
				var _p2418 int = i
				var _rhs2419 int = 1
				if _rhs2419 > 0 && _p2418 > math.MaxInt-_rhs2419 {
					panic("integer overflow")
				}
				if _rhs2419 < 0 && _p2418 < math.MinInt-_rhs2419 {
					panic("integer overflow")
				}
				return _p2418 + _rhs2419
			}(), close))
			// Osty: /tmp/selfhost_merged.osty:17438:13
			func() {
				var _cur2420 int = close
				var _rhs2421 int = 1
				if _rhs2421 > 0 && _cur2420 > math.MaxInt-_rhs2421 {
					panic("integer overflow")
				}
				if _rhs2421 < 0 && _cur2420 < math.MinInt-_rhs2421 {
					panic("integer overflow")
				}
				i = _cur2420 + _rhs2421
			}()
		} else if (kind == "." || kind == "?.") && func() int {
			var _p2422 int = i
			var _rhs2423 int = 1
			if _rhs2423 > 0 && _p2422 > math.MaxInt-_rhs2423 {
				panic("integer overflow")
			}
			if _rhs2423 < 0 && _p2422 < math.MinInt-_rhs2423 {
				panic("integer overflow")
			}
			return _p2422 + _rhs2423
		}() < end && astbridge.TokenIsIdent(astLowerTok(toks, func() int {
			var _p2424 int = i
			var _rhs2425 int = 1
			if _rhs2425 > 0 && _p2424 > math.MaxInt-_rhs2425 {
				panic("integer overflow")
			}
			if _rhs2425 < 0 && _p2424 < math.MinInt-_rhs2425 {
				panic("integer overflow")
			}
			return _p2424 + _rhs2425
		}())) {
			// Osty: /tmp/selfhost_merged.osty:17440:13
			expr = astbridge.FieldExprNode(astbridge.ExprPos(expr, astLowerPos(toks, start)), astbridge.TokenEnd(astLowerTok(toks, func() int {
				var _p2426 int = i
				var _rhs2427 int = 1
				if _rhs2427 > 0 && _p2426 > math.MaxInt-_rhs2427 {
					panic("integer overflow")
				}
				if _rhs2427 < 0 && _p2426 < math.MinInt-_rhs2427 {
					panic("integer overflow")
				}
				return _p2426 + _rhs2427
			}())), expr, astbridge.TokenValue(astLowerTok(toks, func() int {
				var _p2428 int = i
				var _rhs2429 int = 1
				if _rhs2429 > 0 && _p2428 > math.MaxInt-_rhs2429 {
					panic("integer overflow")
				}
				if _rhs2429 < 0 && _p2428 < math.MinInt-_rhs2429 {
					panic("integer overflow")
				}
				return _p2428 + _rhs2429
			}())), kind == "?.")
			// Osty: /tmp/selfhost_merged.osty:17441:13
			func() {
				var _cur2430 int = i
				var _rhs2431 int = 2
				if _rhs2431 > 0 && _cur2430 > math.MaxInt-_rhs2431 {
					panic("integer overflow")
				}
				if _rhs2431 < 0 && _cur2430 < math.MinInt-_rhs2431 {
					panic("integer overflow")
				}
				i = _cur2430 + _rhs2431
			}()
		} else {
			// Osty: /tmp/selfhost_merged.osty:17443:13
			return expr
		}
	}
	return expr
}

// Osty: /tmp/selfhost_merged.osty:17449:1
func astLowerInterpPrimary(toks []astbridge.Token, start int, end int) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:17450:5
	tok := astLowerTok(toks, start)
	_ = tok
	// Osty: /tmp/selfhost_merged.osty:17451:5
	kind := astbridge.TokenKindString(tok)
	_ = kind
	// Osty: /tmp/selfhost_merged.osty:17452:5
	value := astbridge.TokenValue(tok)
	_ = value
	// Osty: /tmp/selfhost_merged.osty:17453:5
	if kind == "IDENT" {
		// Osty: /tmp/selfhost_merged.osty:17454:9
		if value == "true" {
			// Osty: /tmp/selfhost_merged.osty:17455:13
			return astbridge.BoolLitExpr(astLowerPos(toks, start), astLowerEnd(toks, func() int {
				var _p2432 int = start
				var _rhs2433 int = 1
				if _rhs2433 > 0 && _p2432 > math.MaxInt-_rhs2433 {
					panic("integer overflow")
				}
				if _rhs2433 < 0 && _p2432 < math.MinInt-_rhs2433 {
					panic("integer overflow")
				}
				return _p2432 + _rhs2433
			}()), true)
		}
		// Osty: /tmp/selfhost_merged.osty:17457:9
		if value == "false" {
			// Osty: /tmp/selfhost_merged.osty:17458:13
			return astbridge.BoolLitExpr(astLowerPos(toks, start), astLowerEnd(toks, func() int {
				var _p2434 int = start
				var _rhs2435 int = 1
				if _rhs2435 > 0 && _p2434 > math.MaxInt-_rhs2435 {
					panic("integer overflow")
				}
				if _rhs2435 < 0 && _p2434 < math.MinInt-_rhs2435 {
					panic("integer overflow")
				}
				return _p2434 + _rhs2435
			}()), false)
		}
		// Osty: /tmp/selfhost_merged.osty:17460:9
		return astbridge.IdentExpr(astLowerPos(toks, start), astLowerEnd(toks, func() int {
			var _p2436 int = start
			var _rhs2437 int = 1
			if _rhs2437 > 0 && _p2436 > math.MaxInt-_rhs2437 {
				panic("integer overflow")
			}
			if _rhs2437 < 0 && _p2436 < math.MinInt-_rhs2437 {
				panic("integer overflow")
			}
			return _p2436 + _rhs2437
		}()), value)
	}
	// Osty: /tmp/selfhost_merged.osty:17462:5
	if kind == "INT" {
		// Osty: /tmp/selfhost_merged.osty:17463:9
		return astbridge.IntLitExpr(astLowerPos(toks, start), astLowerEnd(toks, func() int {
			var _p2438 int = start
			var _rhs2439 int = 1
			if _rhs2439 > 0 && _p2438 > math.MaxInt-_rhs2439 {
				panic("integer overflow")
			}
			if _rhs2439 < 0 && _p2438 < math.MinInt-_rhs2439 {
				panic("integer overflow")
			}
			return _p2438 + _rhs2439
		}()), value)
	}
	// Osty: /tmp/selfhost_merged.osty:17465:5
	if kind == "FLOAT" {
		// Osty: /tmp/selfhost_merged.osty:17466:9
		return astbridge.FloatLitExpr(astLowerPos(toks, start), astLowerEnd(toks, func() int {
			var _p2440 int = start
			var _rhs2441 int = 1
			if _rhs2441 > 0 && _p2440 > math.MaxInt-_rhs2441 {
				panic("integer overflow")
			}
			if _rhs2441 < 0 && _p2440 < math.MinInt-_rhs2441 {
				panic("integer overflow")
			}
			return _p2440 + _rhs2441
		}()), value)
	}
	// Osty: /tmp/selfhost_merged.osty:17468:5
	if astbridge.TokenIsString(tok) {
		// Osty: /tmp/selfhost_merged.osty:17469:9
		return astbridge.StringLitFromToken(astLowerPos(toks, start), astLowerEnd(toks, func() int {
			var _p2442 int = start
			var _rhs2443 int = 1
			if _rhs2443 > 0 && _p2442 > math.MaxInt-_rhs2443 {
				panic("integer overflow")
			}
			if _rhs2443 < 0 && _p2442 < math.MinInt-_rhs2443 {
				panic("integer overflow")
			}
			return _p2442 + _rhs2443
		}()), tok)
	}
	// Osty: /tmp/selfhost_merged.osty:17471:5
	if kind == "CHAR" {
		// Osty: /tmp/selfhost_merged.osty:17472:9
		return astbridge.CharLitExpr(astLowerPos(toks, start), astLowerEnd(toks, func() int {
			var _p2444 int = start
			var _rhs2445 int = 1
			if _rhs2445 > 0 && _p2444 > math.MaxInt-_rhs2445 {
				panic("integer overflow")
			}
			if _rhs2445 < 0 && _p2444 < math.MinInt-_rhs2445 {
				panic("integer overflow")
			}
			return _p2444 + _rhs2445
		}()), astLowerDecodedLiteral(value))
	}
	// Osty: /tmp/selfhost_merged.osty:17474:5
	if kind == "BYTE" {
		// Osty: /tmp/selfhost_merged.osty:17475:9
		return astbridge.ByteLitExpr(astLowerPos(toks, start), astLowerEnd(toks, func() int {
			var _p2446 int = start
			var _rhs2447 int = 1
			if _rhs2447 > 0 && _p2446 > math.MaxInt-_rhs2447 {
				panic("integer overflow")
			}
			if _rhs2447 < 0 && _p2446 < math.MinInt-_rhs2447 {
				panic("integer overflow")
			}
			return _p2446 + _rhs2447
		}()), astLowerDecodedLiteral(value))
	}
	return astbridge.NilExpr()
}

// Osty: /tmp/selfhost_merged.osty:17480:1
func astLowerInterpTrimStart(toks []astbridge.Token, start int, end int) int {
	// Osty: /tmp/selfhost_merged.osty:17481:5
	i := start
	_ = i
	// Osty: /tmp/selfhost_merged.osty:17482:5
	for i < end && (astbridge.TokenIsNewline(astLowerTok(toks, i)) || astbridge.TokenIsEOF(astLowerTok(toks, i))) {
		// Osty: /tmp/selfhost_merged.osty:17483:9
		func() {
			var _cur2448 int = i
			var _rhs2449 int = 1
			if _rhs2449 > 0 && _cur2448 > math.MaxInt-_rhs2449 {
				panic("integer overflow")
			}
			if _rhs2449 < 0 && _cur2448 < math.MinInt-_rhs2449 {
				panic("integer overflow")
			}
			i = _cur2448 + _rhs2449
		}()
	}
	return i
}

// Osty: /tmp/selfhost_merged.osty:17488:1
func astLowerInterpTrimEnd(toks []astbridge.Token, start int, end int) int {
	// Osty: /tmp/selfhost_merged.osty:17489:5
	i := end
	_ = i
	// Osty: /tmp/selfhost_merged.osty:17490:5
	for i > start && (astbridge.TokenIsNewline(astLowerTok(toks, func() int {
		var _p2450 int = i
		var _rhs2451 int = 1
		if _rhs2451 < 0 && _p2450 > math.MaxInt+_rhs2451 {
			panic("integer overflow")
		}
		if _rhs2451 > 0 && _p2450 < math.MinInt+_rhs2451 {
			panic("integer overflow")
		}
		return _p2450 - _rhs2451
	}())) || astbridge.TokenIsEOF(astLowerTok(toks, func() int {
		var _p2452 int = i
		var _rhs2453 int = 1
		if _rhs2453 < 0 && _p2452 > math.MaxInt+_rhs2453 {
			panic("integer overflow")
		}
		if _rhs2453 > 0 && _p2452 < math.MinInt+_rhs2453 {
			panic("integer overflow")
		}
		return _p2452 - _rhs2453
	}()))) {
		// Osty: /tmp/selfhost_merged.osty:17491:9
		func() {
			var _cur2454 int = i
			var _rhs2455 int = 1
			if _rhs2455 < 0 && _cur2454 > math.MaxInt+_rhs2455 {
				panic("integer overflow")
			}
			if _rhs2455 > 0 && _cur2454 < math.MinInt+_rhs2455 {
				panic("integer overflow")
			}
			i = _cur2454 - _rhs2455
		}()
	}
	return i
}

// Osty: /tmp/selfhost_merged.osty:17496:1
func astLowerInterpSplitTopLevelBinary(toks []astbridge.Token, start int, end int) int {
	// Osty: /tmp/selfhost_merged.osty:17497:5
	best := -1
	_ = best
	// Osty: /tmp/selfhost_merged.osty:17498:5
	bestPrec := 100
	_ = bestPrec
	// Osty: /tmp/selfhost_merged.osty:17499:5
	depth := 0
	_ = depth
	// Osty: /tmp/selfhost_merged.osty:17500:5
	for i := start; i < end; i++ {
		// Osty: /tmp/selfhost_merged.osty:17501:9
		kind := astbridge.TokenKindString(astLowerTok(toks, i))
		_ = kind
		// Osty: /tmp/selfhost_merged.osty:17502:9
		if kind == "(" || kind == "[" || kind == "{" {
			// Osty: /tmp/selfhost_merged.osty:17503:13
			func() {
				var _cur2456 int = depth
				var _rhs2457 int = 1
				if _rhs2457 > 0 && _cur2456 > math.MaxInt-_rhs2457 {
					panic("integer overflow")
				}
				if _rhs2457 < 0 && _cur2456 < math.MinInt-_rhs2457 {
					panic("integer overflow")
				}
				depth = _cur2456 + _rhs2457
			}()
		} else if kind == ")" || kind == "]" || kind == "}" {
			// Osty: /tmp/selfhost_merged.osty:17505:13
			if depth > 0 {
				// Osty: /tmp/selfhost_merged.osty:17506:17
				func() {
					var _cur2458 int = depth
					var _rhs2459 int = 1
					if _rhs2459 < 0 && _cur2458 > math.MaxInt+_rhs2459 {
						panic("integer overflow")
					}
					if _rhs2459 > 0 && _cur2458 < math.MinInt+_rhs2459 {
						panic("integer overflow")
					}
					depth = _cur2458 - _rhs2459
				}()
			}
		} else if depth == 0 {
			// Osty: /tmp/selfhost_merged.osty:17509:13
			prec := astLowerInterpPrecedence(kind)
			_ = prec
			// Osty: /tmp/selfhost_merged.osty:17510:13
			if prec > 0 && prec <= bestPrec {
				// Osty: /tmp/selfhost_merged.osty:17511:17
				best = i
				// Osty: /tmp/selfhost_merged.osty:17512:17
				bestPrec = prec
			}
		}
	}
	return best
}

// Osty: /tmp/selfhost_merged.osty:17519:1
func astLowerInterpPrecedence(kind string) int {
	// Osty: /tmp/selfhost_merged.osty:17520:5
	if kind == "||" {
		// Osty: /tmp/selfhost_merged.osty:17521:9
		return 1
	}
	// Osty: /tmp/selfhost_merged.osty:17523:5
	if kind == "&&" {
		// Osty: /tmp/selfhost_merged.osty:17524:9
		return 2
	}
	// Osty: /tmp/selfhost_merged.osty:17526:5
	if kind == "==" || kind == "!=" || kind == "<" || kind == ">" || kind == "<=" || kind == ">=" {
		// Osty: /tmp/selfhost_merged.osty:17527:9
		return 3
	}
	// Osty: /tmp/selfhost_merged.osty:17529:5
	if kind == "+" || kind == "-" {
		// Osty: /tmp/selfhost_merged.osty:17530:9
		return 4
	}
	// Osty: /tmp/selfhost_merged.osty:17532:5
	if kind == "*" || kind == "/" || kind == "%" {
		// Osty: /tmp/selfhost_merged.osty:17533:9
		return 5
	}
	return 0
}

// Osty: /tmp/selfhost_merged.osty:17538:1
func astLowerInterpFindClose(toks []astbridge.Token, start int, end int, open string, close string) int {
	// Osty: /tmp/selfhost_merged.osty:17539:5
	depth := 0
	_ = depth
	// Osty: /tmp/selfhost_merged.osty:17540:5
	for i := start; i < end; i++ {
		// Osty: /tmp/selfhost_merged.osty:17541:9
		kind := astbridge.TokenKindString(astLowerTok(toks, i))
		_ = kind
		// Osty: /tmp/selfhost_merged.osty:17542:9
		if kind == open {
			// Osty: /tmp/selfhost_merged.osty:17543:13
			func() {
				var _cur2460 int = depth
				var _rhs2461 int = 1
				if _rhs2461 > 0 && _cur2460 > math.MaxInt-_rhs2461 {
					panic("integer overflow")
				}
				if _rhs2461 < 0 && _cur2460 < math.MinInt-_rhs2461 {
					panic("integer overflow")
				}
				depth = _cur2460 + _rhs2461
			}()
		} else if kind == close {
			// Osty: /tmp/selfhost_merged.osty:17545:13
			func() {
				var _cur2462 int = depth
				var _rhs2463 int = 1
				if _rhs2463 < 0 && _cur2462 > math.MaxInt+_rhs2463 {
					panic("integer overflow")
				}
				if _rhs2463 > 0 && _cur2462 < math.MinInt+_rhs2463 {
					panic("integer overflow")
				}
				depth = _cur2462 - _rhs2463
			}()
			// Osty: /tmp/selfhost_merged.osty:17546:13
			if depth == 0 {
				// Osty: /tmp/selfhost_merged.osty:17547:17
				return i
			}
		}
	}
	return -1
}

// Osty: /tmp/selfhost_merged.osty:17554:1
func astLowerInterpArgs(toks []astbridge.Token, start int, end int) []astbridge.Arg {
	// Osty: /tmp/selfhost_merged.osty:17555:5
	out := astbridge.EmptyArgList()
	_ = out
	// Osty: /tmp/selfhost_merged.osty:17556:5
	depth := 0
	_ = depth
	// Osty: /tmp/selfhost_merged.osty:17557:5
	argStart := start
	_ = argStart
	// Osty: /tmp/selfhost_merged.osty:17558:5
	for i := start; i < end; i++ {
		// Osty: /tmp/selfhost_merged.osty:17559:9
		kind := astbridge.TokenKindString(astLowerTok(toks, i))
		_ = kind
		// Osty: /tmp/selfhost_merged.osty:17560:9
		if kind == "(" || kind == "[" || kind == "{" {
			// Osty: /tmp/selfhost_merged.osty:17561:13
			func() {
				var _cur2464 int = depth
				var _rhs2465 int = 1
				if _rhs2465 > 0 && _cur2464 > math.MaxInt-_rhs2465 {
					panic("integer overflow")
				}
				if _rhs2465 < 0 && _cur2464 < math.MinInt-_rhs2465 {
					panic("integer overflow")
				}
				depth = _cur2464 + _rhs2465
			}()
		} else if kind == ")" || kind == "]" || kind == "}" {
			// Osty: /tmp/selfhost_merged.osty:17563:13
			if depth > 0 {
				// Osty: /tmp/selfhost_merged.osty:17564:17
				func() {
					var _cur2466 int = depth
					var _rhs2467 int = 1
					if _rhs2467 < 0 && _cur2466 > math.MaxInt+_rhs2467 {
						panic("integer overflow")
					}
					if _rhs2467 > 0 && _cur2466 < math.MinInt+_rhs2467 {
						panic("integer overflow")
					}
					depth = _cur2466 - _rhs2467
				}()
			}
		} else if kind == "," && depth == 0 {
			// Osty: /tmp/selfhost_merged.osty:17567:13
			out = astLowerInterpAppendArg(toks, out, argStart, i)
			// Osty: /tmp/selfhost_merged.osty:17568:13
			func() {
				var _cur2468 int = i
				var _rhs2469 int = 1
				if _rhs2469 > 0 && _cur2468 > math.MaxInt-_rhs2469 {
					panic("integer overflow")
				}
				if _rhs2469 < 0 && _cur2468 < math.MinInt-_rhs2469 {
					panic("integer overflow")
				}
				argStart = _cur2468 + _rhs2469
			}()
		}
	}
	return astLowerInterpAppendArg(toks, out, argStart, end)
}

// Osty: /tmp/selfhost_merged.osty:17574:1
func astLowerInterpAppendArg(toks []astbridge.Token, args []astbridge.Arg, rawStart int, rawEnd int) []astbridge.Arg {
	// Osty: /tmp/selfhost_merged.osty:17575:5
	out := args
	_ = out
	// Osty: /tmp/selfhost_merged.osty:17576:5
	start := astLowerInterpTrimStart(toks, rawStart, rawEnd)
	_ = start
	// Osty: /tmp/selfhost_merged.osty:17577:5
	end := astLowerInterpTrimEnd(toks, start, rawEnd)
	_ = end
	// Osty: /tmp/selfhost_merged.osty:17578:5
	if start < end {
		// Osty: /tmp/selfhost_merged.osty:17579:9
		func() struct{} {
			out = append(out, astbridge.ArgNode(astLowerPos(toks, start), "", astLowerInterpExpr(toks, start, end)))
			return struct{}{}
		}()
	}
	return out
}

// Osty: /tmp/selfhost_merged.osty:17584:1
func astLowerTok(toks []astbridge.Token, idx int) astbridge.Token {
	return astbridge.TokenAt(toks, idx)
}

// Osty: /tmp/selfhost_merged.osty:17588:1
func astLowerPos(toks []astbridge.Token, idx int) astbridge.Pos {
	return astbridge.TokenPos(astLowerTok(toks, idx))
}

// Osty: /tmp/selfhost_merged.osty:17592:1
func astLowerEnd(toks []astbridge.Token, idx int) astbridge.Pos {
	// Osty: /tmp/selfhost_merged.osty:17593:5
	if idx <= 0 {
		// Osty: /tmp/selfhost_merged.osty:17594:9
		return astbridge.TokenEnd(astLowerTok(toks, 0))
	}
	return astbridge.TokenEnd(astLowerTok(toks, func() int {
		var _p2470 int = idx
		var _rhs2471 int = 1
		if _rhs2471 < 0 && _p2470 > math.MaxInt+_rhs2471 {
			panic("integer overflow")
		}
		if _rhs2471 > 0 && _p2470 < math.MinInt+_rhs2471 {
			panic("integer overflow")
		}
		return _p2470 - _rhs2471
	}()))
}

// Osty: /tmp/selfhost_merged.osty:17599:1
func astLowerNodePos(toks []astbridge.Token, n *AstNode) astbridge.Pos {
	return astLowerPos(toks, n.start)
}

// Osty: /tmp/selfhost_merged.osty:17603:1
func astLowerNodeEnd(toks []astbridge.Token, n *AstNode) astbridge.Pos {
	return astLowerEnd(toks, n.end)
}

// Osty: /tmp/selfhost_merged.osty:17607:1
func astLowerMutPos(toks []astbridge.Token, n *AstNode) astbridge.Pos {
	// Osty: /tmp/selfhost_merged.osty:17608:5
	if !(n.flags == 1) {
		// Osty: /tmp/selfhost_merged.osty:17609:9
		return astbridge.ZeroPos()
	}
	// Osty: /tmp/selfhost_merged.osty:17611:5
	for i := n.start; i < n.end; i++ {
		// Osty: /tmp/selfhost_merged.osty:17612:9
		if astbridge.TokenKindString(astLowerTok(toks, i)) == "mut" {
			// Osty: /tmp/selfhost_merged.osty:17613:13
			return astbridge.TokenPos(astLowerTok(toks, i))
		}
	}
	return astbridge.ZeroPos()
}

// Osty: /tmp/selfhost_merged.osty:17619:1
func astLowerDoc(toks []astbridge.Token, idx int) string {
	// Osty: /tmp/selfhost_merged.osty:17620:5
	direct := astbridge.TokenLeadingDoc(astLowerTok(toks, idx))
	_ = direct
	// Osty: /tmp/selfhost_merged.osty:17621:5
	if direct != "" {
		// Osty: /tmp/selfhost_merged.osty:17622:9
		return direct
	}
	// Osty: /tmp/selfhost_merged.osty:17624:5
	if idx > 0 && astbridge.TokenIsPub(astLowerTok(toks, func() int {
		var _p2472 int = idx
		var _rhs2473 int = 1
		if _rhs2473 < 0 && _p2472 > math.MaxInt+_rhs2473 {
			panic("integer overflow")
		}
		if _rhs2473 > 0 && _p2472 < math.MinInt+_rhs2473 {
			panic("integer overflow")
		}
		return _p2472 - _rhs2473
	}())) {
		// Osty: /tmp/selfhost_merged.osty:17625:9
		return astbridge.TokenLeadingDoc(astLowerTok(toks, func() int {
			var _p2474 int = idx
			var _rhs2475 int = 1
			if _rhs2475 < 0 && _p2474 > math.MaxInt+_rhs2475 {
				panic("integer overflow")
			}
			if _rhs2475 > 0 && _p2474 < math.MinInt+_rhs2475 {
				panic("integer overflow")
			}
			return _p2474 - _rhs2475
		}()))
	}
	return ""
}

// Osty: /tmp/selfhost_merged.osty:17630:1
func astLowerKind(kind FrontTokenKind) astbridge.Kind {
	// Osty: /tmp/selfhost_merged.osty:17631:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontEOF{})) {
		// Osty: /tmp/selfhost_merged.osty:17631:27
		return astbridge.KindEOF()
	}
	// Osty: /tmp/selfhost_merged.osty:17632:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontIllegal{})) {
		// Osty: /tmp/selfhost_merged.osty:17632:31
		return astbridge.KindIllegal()
	}
	// Osty: /tmp/selfhost_merged.osty:17633:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontNewline{})) {
		// Osty: /tmp/selfhost_merged.osty:17633:31
		return astbridge.KindNewline()
	}
	// Osty: /tmp/selfhost_merged.osty:17634:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontIdent{})) {
		// Osty: /tmp/selfhost_merged.osty:17634:29
		return astbridge.KindIdent()
	}
	// Osty: /tmp/selfhost_merged.osty:17635:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontInt{})) {
		// Osty: /tmp/selfhost_merged.osty:17635:27
		return astbridge.KindInt()
	}
	// Osty: /tmp/selfhost_merged.osty:17636:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontFloat{})) {
		// Osty: /tmp/selfhost_merged.osty:17636:29
		return astbridge.KindFloat()
	}
	// Osty: /tmp/selfhost_merged.osty:17637:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontChar{})) {
		// Osty: /tmp/selfhost_merged.osty:17637:28
		return astbridge.KindChar()
	}
	// Osty: /tmp/selfhost_merged.osty:17638:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontByte{})) {
		// Osty: /tmp/selfhost_merged.osty:17638:28
		return astbridge.KindByte()
	}
	// Osty: /tmp/selfhost_merged.osty:17639:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontString{})) {
		// Osty: /tmp/selfhost_merged.osty:17639:30
		return astbridge.KindString()
	}
	// Osty: /tmp/selfhost_merged.osty:17640:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontRawString{})) {
		// Osty: /tmp/selfhost_merged.osty:17640:33
		return astbridge.KindRawString()
	}
	// Osty: /tmp/selfhost_merged.osty:17641:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontFn{})) {
		// Osty: /tmp/selfhost_merged.osty:17641:26
		return astbridge.KindFn()
	}
	// Osty: /tmp/selfhost_merged.osty:17642:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontStruct{})) {
		// Osty: /tmp/selfhost_merged.osty:17642:30
		return astbridge.KindStruct()
	}
	// Osty: /tmp/selfhost_merged.osty:17643:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontEnum{})) {
		// Osty: /tmp/selfhost_merged.osty:17643:28
		return astbridge.KindEnum()
	}
	// Osty: /tmp/selfhost_merged.osty:17644:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontInterface{})) {
		// Osty: /tmp/selfhost_merged.osty:17644:33
		return astbridge.KindInterface()
	}
	// Osty: /tmp/selfhost_merged.osty:17645:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontType{})) {
		// Osty: /tmp/selfhost_merged.osty:17645:28
		return astbridge.KindType()
	}
	// Osty: /tmp/selfhost_merged.osty:17646:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontLet{})) {
		// Osty: /tmp/selfhost_merged.osty:17646:27
		return astbridge.KindLet()
	}
	// Osty: /tmp/selfhost_merged.osty:17647:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontMut{})) {
		// Osty: /tmp/selfhost_merged.osty:17647:27
		return astbridge.KindMut()
	}
	// Osty: /tmp/selfhost_merged.osty:17648:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontPub{})) {
		// Osty: /tmp/selfhost_merged.osty:17648:27
		return astbridge.KindPub()
	}
	// Osty: /tmp/selfhost_merged.osty:17649:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontIf{})) {
		// Osty: /tmp/selfhost_merged.osty:17649:26
		return astbridge.KindIf()
	}
	// Osty: /tmp/selfhost_merged.osty:17650:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontElse{})) {
		// Osty: /tmp/selfhost_merged.osty:17650:28
		return astbridge.KindElse()
	}
	// Osty: /tmp/selfhost_merged.osty:17651:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontMatch{})) {
		// Osty: /tmp/selfhost_merged.osty:17651:29
		return astbridge.KindMatch()
	}
	// Osty: /tmp/selfhost_merged.osty:17652:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontFor{})) {
		// Osty: /tmp/selfhost_merged.osty:17652:27
		return astbridge.KindFor()
	}
	// Osty: /tmp/selfhost_merged.osty:17653:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontBreak{})) {
		// Osty: /tmp/selfhost_merged.osty:17653:29
		return astbridge.KindBreak()
	}
	// Osty: /tmp/selfhost_merged.osty:17654:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontContinue{})) {
		// Osty: /tmp/selfhost_merged.osty:17654:32
		return astbridge.KindContinue()
	}
	// Osty: /tmp/selfhost_merged.osty:17655:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontReturn{})) {
		// Osty: /tmp/selfhost_merged.osty:17655:30
		return astbridge.KindReturn()
	}
	// Osty: /tmp/selfhost_merged.osty:17656:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontUse{})) {
		// Osty: /tmp/selfhost_merged.osty:17656:27
		return astbridge.KindUse()
	}
	// Osty: /tmp/selfhost_merged.osty:17657:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontDefer{})) {
		// Osty: /tmp/selfhost_merged.osty:17657:29
		return astbridge.KindDefer()
	}
	// Osty: /tmp/selfhost_merged.osty:17658:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontLParen{})) {
		// Osty: /tmp/selfhost_merged.osty:17658:30
		return astbridge.KindLParen()
	}
	// Osty: /tmp/selfhost_merged.osty:17659:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontRParen{})) {
		// Osty: /tmp/selfhost_merged.osty:17659:30
		return astbridge.KindRParen()
	}
	// Osty: /tmp/selfhost_merged.osty:17660:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontLBrace{})) {
		// Osty: /tmp/selfhost_merged.osty:17660:30
		return astbridge.KindLBrace()
	}
	// Osty: /tmp/selfhost_merged.osty:17661:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontRBrace{})) {
		// Osty: /tmp/selfhost_merged.osty:17661:30
		return astbridge.KindRBrace()
	}
	// Osty: /tmp/selfhost_merged.osty:17662:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontLBracket{})) {
		// Osty: /tmp/selfhost_merged.osty:17662:32
		return astbridge.KindLBracket()
	}
	// Osty: /tmp/selfhost_merged.osty:17663:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontRBracket{})) {
		// Osty: /tmp/selfhost_merged.osty:17663:32
		return astbridge.KindRBracket()
	}
	// Osty: /tmp/selfhost_merged.osty:17664:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontComma{})) {
		// Osty: /tmp/selfhost_merged.osty:17664:29
		return astbridge.KindComma()
	}
	// Osty: /tmp/selfhost_merged.osty:17665:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontColon{})) {
		// Osty: /tmp/selfhost_merged.osty:17665:29
		return astbridge.KindColon()
	}
	// Osty: /tmp/selfhost_merged.osty:17666:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontSemicolon{})) {
		// Osty: /tmp/selfhost_merged.osty:17666:33
		return astbridge.KindSemicolon()
	}
	// Osty: /tmp/selfhost_merged.osty:17667:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontDot{})) {
		// Osty: /tmp/selfhost_merged.osty:17667:27
		return astbridge.KindDot()
	}
	// Osty: /tmp/selfhost_merged.osty:17668:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontPlus{})) {
		// Osty: /tmp/selfhost_merged.osty:17668:28
		return astbridge.KindPlus()
	}
	// Osty: /tmp/selfhost_merged.osty:17669:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontMinus{})) {
		// Osty: /tmp/selfhost_merged.osty:17669:29
		return astbridge.KindMinus()
	}
	// Osty: /tmp/selfhost_merged.osty:17670:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontStar{})) {
		// Osty: /tmp/selfhost_merged.osty:17670:28
		return astbridge.KindStar()
	}
	// Osty: /tmp/selfhost_merged.osty:17671:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontSlash{})) {
		// Osty: /tmp/selfhost_merged.osty:17671:29
		return astbridge.KindSlash()
	}
	// Osty: /tmp/selfhost_merged.osty:17672:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontPercent{})) {
		// Osty: /tmp/selfhost_merged.osty:17672:31
		return astbridge.KindPercent()
	}
	// Osty: /tmp/selfhost_merged.osty:17673:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17673:26
		return astbridge.KindEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17674:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontNeq{})) {
		// Osty: /tmp/selfhost_merged.osty:17674:27
		return astbridge.KindNeq()
	}
	// Osty: /tmp/selfhost_merged.osty:17675:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontLt{})) {
		// Osty: /tmp/selfhost_merged.osty:17675:26
		return astbridge.KindLt()
	}
	// Osty: /tmp/selfhost_merged.osty:17676:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontGt{})) {
		// Osty: /tmp/selfhost_merged.osty:17676:26
		return astbridge.KindGt()
	}
	// Osty: /tmp/selfhost_merged.osty:17677:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontLeq{})) {
		// Osty: /tmp/selfhost_merged.osty:17677:27
		return astbridge.KindLeq()
	}
	// Osty: /tmp/selfhost_merged.osty:17678:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontGeq{})) {
		// Osty: /tmp/selfhost_merged.osty:17678:27
		return astbridge.KindGeq()
	}
	// Osty: /tmp/selfhost_merged.osty:17679:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontAnd{})) {
		// Osty: /tmp/selfhost_merged.osty:17679:27
		return astbridge.KindAnd()
	}
	// Osty: /tmp/selfhost_merged.osty:17680:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontOr{})) {
		// Osty: /tmp/selfhost_merged.osty:17680:26
		return astbridge.KindOr()
	}
	// Osty: /tmp/selfhost_merged.osty:17681:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontNot{})) {
		// Osty: /tmp/selfhost_merged.osty:17681:27
		return astbridge.KindNot()
	}
	// Osty: /tmp/selfhost_merged.osty:17682:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontBitAnd{})) {
		// Osty: /tmp/selfhost_merged.osty:17682:30
		return astbridge.KindBitAnd()
	}
	// Osty: /tmp/selfhost_merged.osty:17683:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontBitOr{})) {
		// Osty: /tmp/selfhost_merged.osty:17683:29
		return astbridge.KindBitOr()
	}
	// Osty: /tmp/selfhost_merged.osty:17684:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontBitXor{})) {
		// Osty: /tmp/selfhost_merged.osty:17684:30
		return astbridge.KindBitXor()
	}
	// Osty: /tmp/selfhost_merged.osty:17685:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontBitNot{})) {
		// Osty: /tmp/selfhost_merged.osty:17685:30
		return astbridge.KindBitNot()
	}
	// Osty: /tmp/selfhost_merged.osty:17686:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontShl{})) {
		// Osty: /tmp/selfhost_merged.osty:17686:27
		return astbridge.KindShl()
	}
	// Osty: /tmp/selfhost_merged.osty:17687:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontShr{})) {
		// Osty: /tmp/selfhost_merged.osty:17687:27
		return astbridge.KindShr()
	}
	// Osty: /tmp/selfhost_merged.osty:17688:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontAssign{})) {
		// Osty: /tmp/selfhost_merged.osty:17688:30
		return astbridge.KindAssign()
	}
	// Osty: /tmp/selfhost_merged.osty:17689:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontPlusEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17689:30
		return astbridge.KindPlusEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17690:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontMinusEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17690:31
		return astbridge.KindMinusEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17691:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontStarEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17691:30
		return astbridge.KindStarEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17692:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontSlashEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17692:31
		return astbridge.KindSlashEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17693:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontPercentEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17693:33
		return astbridge.KindPercentEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17694:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontBitAndEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17694:32
		return astbridge.KindBitAndEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17695:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontBitOrEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17695:31
		return astbridge.KindBitOrEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17696:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontBitXorEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17696:32
		return astbridge.KindBitXorEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17697:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontShlEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17697:29
		return astbridge.KindShlEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17698:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontShrEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17698:29
		return astbridge.KindShrEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17699:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontQuestion{})) {
		// Osty: /tmp/selfhost_merged.osty:17699:32
		return astbridge.KindQuestion()
	}
	// Osty: /tmp/selfhost_merged.osty:17700:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontQDot{})) {
		// Osty: /tmp/selfhost_merged.osty:17700:28
		return astbridge.KindQDot()
	}
	// Osty: /tmp/selfhost_merged.osty:17701:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontQQ{})) {
		// Osty: /tmp/selfhost_merged.osty:17701:26
		return astbridge.KindQQ()
	}
	// Osty: /tmp/selfhost_merged.osty:17702:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontDotDot{})) {
		// Osty: /tmp/selfhost_merged.osty:17702:30
		return astbridge.KindDotDot()
	}
	// Osty: /tmp/selfhost_merged.osty:17703:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontDotDotEq{})) {
		// Osty: /tmp/selfhost_merged.osty:17703:32
		return astbridge.KindDotDotEq()
	}
	// Osty: /tmp/selfhost_merged.osty:17704:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontArrow{})) {
		// Osty: /tmp/selfhost_merged.osty:17704:29
		return astbridge.KindArrow()
	}
	// Osty: /tmp/selfhost_merged.osty:17705:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontChanArrow{})) {
		// Osty: /tmp/selfhost_merged.osty:17705:33
		return astbridge.KindChanArrow()
	}
	// Osty: /tmp/selfhost_merged.osty:17706:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontColonColon{})) {
		// Osty: /tmp/selfhost_merged.osty:17706:34
		return astbridge.KindColonColon()
	}
	// Osty: /tmp/selfhost_merged.osty:17707:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontUnderscore{})) {
		// Osty: /tmp/selfhost_merged.osty:17707:34
		return astbridge.KindUnderscore()
	}
	// Osty: /tmp/selfhost_merged.osty:17708:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontAt{})) {
		// Osty: /tmp/selfhost_merged.osty:17708:26
		return astbridge.KindAt()
	}
	// Osty: /tmp/selfhost_merged.osty:17709:5
	if ostyEqual(kind, FrontTokenKind(&FrontTokenKind_FrontHash{})) {
		// Osty: /tmp/selfhost_merged.osty:17709:28
		return astbridge.KindHash()
	}
	return astbridge.KindIllegal()
}

// Osty: /tmp/selfhost_merged.osty:17713:1
func astLowerSplitPath(raw string) []string {
	// Osty: /tmp/selfhost_merged.osty:17714:5
	if raw == "" {
		// Osty: /tmp/selfhost_merged.osty:17715:9
		return make([]string, 0, 1)
	}
	// Osty: /tmp/selfhost_merged.osty:17717:5
	if strings.Count(raw, "/") > 0 {
		// Osty: /tmp/selfhost_merged.osty:17718:9
		return []string{raw}
	}
	return strings.Split(raw, ".")
}

// Osty: /tmp/selfhost_merged.osty:17723:1
func astLowerUnquoteMaybe(s string) string {
	return astLowerStringContent(s)
}

// Osty: /tmp/selfhost_merged.osty:17727:1
func astLowerStringContent(s string) string {
	// Osty: /tmp/selfhost_merged.osty:17728:5
	if strings.HasPrefix(s, "r\"\"\"") && strings.HasSuffix(s, "\"\"\"") {
		// Osty: /tmp/selfhost_merged.osty:17729:9
		return strings.TrimSuffix(strings.TrimPrefix(s, "r\"\"\""), "\"\"\"")
	}
	// Osty: /tmp/selfhost_merged.osty:17731:5
	if strings.HasPrefix(s, "\"\"\"") && strings.HasSuffix(s, "\"\"\"") {
		// Osty: /tmp/selfhost_merged.osty:17732:9
		return astLowerDecodeEscapes(strings.TrimSuffix(strings.TrimPrefix(s, "\"\"\""), "\"\"\""))
	}
	// Osty: /tmp/selfhost_merged.osty:17734:5
	if strings.HasPrefix(s, "r\"") && strings.HasSuffix(s, "\"") {
		// Osty: /tmp/selfhost_merged.osty:17735:9
		return strings.TrimSuffix(strings.TrimPrefix(s, "r\""), "\"")
	}
	// Osty: /tmp/selfhost_merged.osty:17737:5
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		// Osty: /tmp/selfhost_merged.osty:17738:9
		return astLowerDecodeEscapes(strings.TrimSuffix(strings.TrimPrefix(s, "\""), "\""))
	}
	return s
}

// Osty: /tmp/selfhost_merged.osty:17743:1
func astLowerDecodedLiteral(s string) string {
	// Osty: /tmp/selfhost_merged.osty:17744:5
	raw := s
	_ = raw
	// Osty: /tmp/selfhost_merged.osty:17745:5
	if strings.HasPrefix(raw, "b") {
		// Osty: /tmp/selfhost_merged.osty:17746:9
		raw = strings.TrimPrefix(raw, "b")
	}
	// Osty: /tmp/selfhost_merged.osty:17748:5
	if strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") {
		// Osty: /tmp/selfhost_merged.osty:17749:9
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "'"), "'")
	}
	return astLowerDecodeEscapes(raw)
}

// Osty: /tmp/selfhost_merged.osty:17754:1
func astLowerDecodeEscapes(s string) string {
	// Osty: /tmp/selfhost_merged.osty:17755:5
	units := strings.Split(s, "")
	_ = units
	// Osty: /tmp/selfhost_merged.osty:17756:5
	out := ""
	_ = out
	// Osty: /tmp/selfhost_merged.osty:17757:5
	i := 0
	_ = i
	// Osty: /tmp/selfhost_merged.osty:17758:5
	for i < astLowerStringUnitCount(units) {
		// Osty: /tmp/selfhost_merged.osty:17759:9
		unit := frontUnitAt(units, i)
		_ = unit
		// Osty: /tmp/selfhost_merged.osty:17760:9
		if unit != "\\" {
			// Osty: /tmp/selfhost_merged.osty:17761:13
			out = fmt.Sprintf("%s%s", ostyToString(out), ostyToString(unit))
			// Osty: /tmp/selfhost_merged.osty:17762:13
			func() {
				var _cur2476 int = i
				var _rhs2477 int = 1
				if _rhs2477 > 0 && _cur2476 > math.MaxInt-_rhs2477 {
					panic("integer overflow")
				}
				if _rhs2477 < 0 && _cur2476 < math.MinInt-_rhs2477 {
					panic("integer overflow")
				}
				i = _cur2476 + _rhs2477
			}()
		} else if func() int {
			var _p2478 int = i
			var _rhs2479 int = 1
			if _rhs2479 > 0 && _p2478 > math.MaxInt-_rhs2479 {
				panic("integer overflow")
			}
			if _rhs2479 < 0 && _p2478 < math.MinInt-_rhs2479 {
				panic("integer overflow")
			}
			return _p2478 + _rhs2479
		}() >= astLowerStringUnitCount(units) {
			// Osty: /tmp/selfhost_merged.osty:17764:13
			out = fmt.Sprintf("%s\\", ostyToString(out))
			// Osty: /tmp/selfhost_merged.osty:17765:13
			func() {
				var _cur2480 int = i
				var _rhs2481 int = 1
				if _rhs2481 > 0 && _cur2480 > math.MaxInt-_rhs2481 {
					panic("integer overflow")
				}
				if _rhs2481 < 0 && _cur2480 < math.MinInt-_rhs2481 {
					panic("integer overflow")
				}
				i = _cur2480 + _rhs2481
			}()
		} else {
			// Osty: /tmp/selfhost_merged.osty:17767:13
			next := frontUnitAt(units, func() int {
				var _p2482 int = i
				var _rhs2483 int = 1
				if _rhs2483 > 0 && _p2482 > math.MaxInt-_rhs2483 {
					panic("integer overflow")
				}
				if _rhs2483 < 0 && _p2482 < math.MinInt-_rhs2483 {
					panic("integer overflow")
				}
				return _p2482 + _rhs2483
			}())
			_ = next
			// Osty: /tmp/selfhost_merged.osty:17768:13
			if next == "n" {
				// Osty: /tmp/selfhost_merged.osty:17769:17
				out = fmt.Sprintf("%s\n", ostyToString(out))
				// Osty: /tmp/selfhost_merged.osty:17770:17
				func() {
					var _cur2484 int = i
					var _rhs2485 int = 2
					if _rhs2485 > 0 && _cur2484 > math.MaxInt-_rhs2485 {
						panic("integer overflow")
					}
					if _rhs2485 < 0 && _cur2484 < math.MinInt-_rhs2485 {
						panic("integer overflow")
					}
					i = _cur2484 + _rhs2485
				}()
			} else if next == "r" {
				// Osty: /tmp/selfhost_merged.osty:17772:17
				out = fmt.Sprintf("%s\r", ostyToString(out))
				// Osty: /tmp/selfhost_merged.osty:17773:17
				func() {
					var _cur2486 int = i
					var _rhs2487 int = 2
					if _rhs2487 > 0 && _cur2486 > math.MaxInt-_rhs2487 {
						panic("integer overflow")
					}
					if _rhs2487 < 0 && _cur2486 < math.MinInt-_rhs2487 {
						panic("integer overflow")
					}
					i = _cur2486 + _rhs2487
				}()
			} else if next == "t" {
				// Osty: /tmp/selfhost_merged.osty:17775:17
				out = fmt.Sprintf("%s\t", ostyToString(out))
				// Osty: /tmp/selfhost_merged.osty:17776:17
				func() {
					var _cur2488 int = i
					var _rhs2489 int = 2
					if _rhs2489 > 0 && _cur2488 > math.MaxInt-_rhs2489 {
						panic("integer overflow")
					}
					if _rhs2489 < 0 && _cur2488 < math.MinInt-_rhs2489 {
						panic("integer overflow")
					}
					i = _cur2488 + _rhs2489
				}()
			} else if next == "0" {
				// Osty: /tmp/selfhost_merged.osty:17778:17
				out = fmt.Sprintf("%s%s", ostyToString(out), ostyToString(astbridge.RuneString(0)))
				// Osty: /tmp/selfhost_merged.osty:17779:17
				func() {
					var _cur2490 int = i
					var _rhs2491 int = 2
					if _rhs2491 > 0 && _cur2490 > math.MaxInt-_rhs2491 {
						panic("integer overflow")
					}
					if _rhs2491 < 0 && _cur2490 < math.MinInt-_rhs2491 {
						panic("integer overflow")
					}
					i = _cur2490 + _rhs2491
				}()
			} else if next == "x" {
				// Osty: /tmp/selfhost_merged.osty:17781:17
				high := frontHexValue(frontUnitAt(units, func() int {
					var _p2492 int = i
					var _rhs2493 int = 2
					if _rhs2493 > 0 && _p2492 > math.MaxInt-_rhs2493 {
						panic("integer overflow")
					}
					if _rhs2493 < 0 && _p2492 < math.MinInt-_rhs2493 {
						panic("integer overflow")
					}
					return _p2492 + _rhs2493
				}()))
				_ = high
				// Osty: /tmp/selfhost_merged.osty:17782:17
				low := frontHexValue(frontUnitAt(units, func() int {
					var _p2494 int = i
					var _rhs2495 int = 3
					if _rhs2495 > 0 && _p2494 > math.MaxInt-_rhs2495 {
						panic("integer overflow")
					}
					if _rhs2495 < 0 && _p2494 < math.MinInt-_rhs2495 {
						panic("integer overflow")
					}
					return _p2494 + _rhs2495
				}()))
				_ = low
				// Osty: /tmp/selfhost_merged.osty:17783:17
				if func() int {
					var _p2496 int = i
					var _rhs2497 int = 3
					if _rhs2497 > 0 && _p2496 > math.MaxInt-_rhs2497 {
						panic("integer overflow")
					}
					if _rhs2497 < 0 && _p2496 < math.MinInt-_rhs2497 {
						panic("integer overflow")
					}
					return _p2496 + _rhs2497
				}() < astLowerStringUnitCount(units) && high >= 0 && low >= 0 {
					// Osty: /tmp/selfhost_merged.osty:17784:21
					var value int = func() int {
						var _p2498 int = (func() int {
							var _p2499 int = high
							var _rhs2500 int = 16
							if _p2499 != 0 && _rhs2500 != 0 {
								if _p2499 == int(-1) && _rhs2500 == math.MinInt {
									panic("integer overflow")
								}
								if _rhs2500 == int(-1) && _p2499 == math.MinInt {
									panic("integer overflow")
								}
								if _p2499 > 0 {
									if _rhs2500 > 0 && _p2499 > math.MaxInt/_rhs2500 {
										panic("integer overflow")
									}
									if _rhs2500 < 0 && _rhs2500 < math.MinInt/_p2499 {
										panic("integer overflow")
									}
								} else {
									if _rhs2500 > 0 && _p2499 < math.MinInt/_rhs2500 {
										panic("integer overflow")
									}
									if _rhs2500 < 0 && _p2499 < math.MaxInt/_rhs2500 {
										panic("integer overflow")
									}
								}
							}
							return _p2499 * _rhs2500
						}())
						var _rhs2501 int = low
						if _rhs2501 > 0 && _p2498 > math.MaxInt-_rhs2501 {
							panic("integer overflow")
						}
						if _rhs2501 < 0 && _p2498 < math.MinInt-_rhs2501 {
							panic("integer overflow")
						}
						return _p2498 + _rhs2501
					}()
					_ = value
					// Osty: /tmp/selfhost_merged.osty:17785:21
					out = fmt.Sprintf("%s%s", ostyToString(out), ostyToString(astbridge.RuneString(value)))
					// Osty: /tmp/selfhost_merged.osty:17786:21
					func() {
						var _cur2502 int = i
						var _rhs2503 int = 4
						if _rhs2503 > 0 && _cur2502 > math.MaxInt-_rhs2503 {
							panic("integer overflow")
						}
						if _rhs2503 < 0 && _cur2502 < math.MinInt-_rhs2503 {
							panic("integer overflow")
						}
						i = _cur2502 + _rhs2503
					}()
				} else {
					// Osty: /tmp/selfhost_merged.osty:17788:21
					out = fmt.Sprintf("%s%s", ostyToString(out), ostyToString(next))
					// Osty: /tmp/selfhost_merged.osty:17789:21
					func() {
						var _cur2504 int = i
						var _rhs2505 int = 2
						if _rhs2505 > 0 && _cur2504 > math.MaxInt-_rhs2505 {
							panic("integer overflow")
						}
						if _rhs2505 < 0 && _cur2504 < math.MinInt-_rhs2505 {
							panic("integer overflow")
						}
						i = _cur2504 + _rhs2505
					}()
				}
			} else if next == "u" && frontUnitAt(units, func() int {
				var _p2506 int = i
				var _rhs2507 int = 2
				if _rhs2507 > 0 && _p2506 > math.MaxInt-_rhs2507 {
					panic("integer overflow")
				}
				if _rhs2507 < 0 && _p2506 < math.MinInt-_rhs2507 {
					panic("integer overflow")
				}
				return _p2506 + _rhs2507
			}()) == "{" {
				// Osty: /tmp/selfhost_merged.osty:17792:17
				parsed := astLowerDecodeUnicodeEscape(units, func() int {
					var _p2508 int = i
					var _rhs2509 int = 3
					if _rhs2509 > 0 && _p2508 > math.MaxInt-_rhs2509 {
						panic("integer overflow")
					}
					if _rhs2509 < 0 && _p2508 < math.MinInt-_rhs2509 {
						panic("integer overflow")
					}
					return _p2508 + _rhs2509
				}())
				_ = parsed
				// Osty: /tmp/selfhost_merged.osty:17793:17
				if parsed.consumed > 0 {
					// Osty: /tmp/selfhost_merged.osty:17794:21
					out = fmt.Sprintf("%s%s", ostyToString(out), ostyToString(astbridge.RuneString(parsed.value)))
					// Osty: /tmp/selfhost_merged.osty:17795:21
					func() {
						var _cur2510 int = func() int {
							var _p2512 int = i
							var _rhs2513 int = 3
							if _rhs2513 > 0 && _p2512 > math.MaxInt-_rhs2513 {
								panic("integer overflow")
							}
							if _rhs2513 < 0 && _p2512 < math.MinInt-_rhs2513 {
								panic("integer overflow")
							}
							return _p2512 + _rhs2513
						}()
						var _rhs2511 int = parsed.consumed
						if _rhs2511 > 0 && _cur2510 > math.MaxInt-_rhs2511 {
							panic("integer overflow")
						}
						if _rhs2511 < 0 && _cur2510 < math.MinInt-_rhs2511 {
							panic("integer overflow")
						}
						i = _cur2510 + _rhs2511
					}()
				} else {
					// Osty: /tmp/selfhost_merged.osty:17797:21
					out = fmt.Sprintf("%s%s", ostyToString(out), ostyToString(next))
					// Osty: /tmp/selfhost_merged.osty:17798:21
					func() {
						var _cur2514 int = i
						var _rhs2515 int = 2
						if _rhs2515 > 0 && _cur2514 > math.MaxInt-_rhs2515 {
							panic("integer overflow")
						}
						if _rhs2515 < 0 && _cur2514 < math.MinInt-_rhs2515 {
							panic("integer overflow")
						}
						i = _cur2514 + _rhs2515
					}()
				}
			} else {
				// Osty: /tmp/selfhost_merged.osty:17801:17
				out = fmt.Sprintf("%s%s", ostyToString(out), ostyToString(next))
				// Osty: /tmp/selfhost_merged.osty:17802:17
				func() {
					var _cur2516 int = i
					var _rhs2517 int = 2
					if _rhs2517 > 0 && _cur2516 > math.MaxInt-_rhs2517 {
						panic("integer overflow")
					}
					if _rhs2517 < 0 && _cur2516 < math.MinInt-_rhs2517 {
						panic("integer overflow")
					}
					i = _cur2516 + _rhs2517
				}()
			}
		}
	}
	return out
}

// Osty: /tmp/selfhost_merged.osty:17809:1
type AstLowerUnicodeEscape struct {
	value    int
	consumed int
}

// Osty: /tmp/selfhost_merged.osty:17814:1
func astLowerDecodeUnicodeEscape(units []string, start int) *AstLowerUnicodeEscape {
	// Osty: /tmp/selfhost_merged.osty:17815:5
	value := 0
	_ = value
	// Osty: /tmp/selfhost_merged.osty:17816:5
	consumed := 0
	_ = consumed
	// Osty: /tmp/selfhost_merged.osty:17817:5
	total := astLowerStringUnitCount(units)
	_ = total
	// Osty: /tmp/selfhost_merged.osty:17818:5
	for i := start; i < total; i++ {
		// Osty: /tmp/selfhost_merged.osty:17819:9
		unit := frontUnitAt(units, i)
		_ = unit
		// Osty: /tmp/selfhost_merged.osty:17820:9
		if unit == "}" {
			// Osty: /tmp/selfhost_merged.osty:17821:13
			if consumed == 0 {
				// Osty: /tmp/selfhost_merged.osty:17822:17
				return &AstLowerUnicodeEscape{value: 0, consumed: 0}
			}
			// Osty: /tmp/selfhost_merged.osty:17824:13
			return &AstLowerUnicodeEscape{value: value, consumed: func() int {
				var _p2518 int = consumed
				var _rhs2519 int = 1
				if _rhs2519 > 0 && _p2518 > math.MaxInt-_rhs2519 {
					panic("integer overflow")
				}
				if _rhs2519 < 0 && _p2518 < math.MinInt-_rhs2519 {
					panic("integer overflow")
				}
				return _p2518 + _rhs2519
			}()}
		}
		// Osty: /tmp/selfhost_merged.osty:17826:9
		digit := frontHexValue(unit)
		_ = digit
		// Osty: /tmp/selfhost_merged.osty:17827:9
		if digit < 0 {
			// Osty: /tmp/selfhost_merged.osty:17828:13
			return &AstLowerUnicodeEscape{value: 0, consumed: 0}
		}
		// Osty: /tmp/selfhost_merged.osty:17830:9
		func() {
			var _cur2520 int = (func() int {
				var _p2522 int = value
				var _rhs2523 int = 16
				if _p2522 != 0 && _rhs2523 != 0 {
					if _p2522 == int(-1) && _rhs2523 == math.MinInt {
						panic("integer overflow")
					}
					if _rhs2523 == int(-1) && _p2522 == math.MinInt {
						panic("integer overflow")
					}
					if _p2522 > 0 {
						if _rhs2523 > 0 && _p2522 > math.MaxInt/_rhs2523 {
							panic("integer overflow")
						}
						if _rhs2523 < 0 && _rhs2523 < math.MinInt/_p2522 {
							panic("integer overflow")
						}
					} else {
						if _rhs2523 > 0 && _p2522 < math.MinInt/_rhs2523 {
							panic("integer overflow")
						}
						if _rhs2523 < 0 && _p2522 < math.MaxInt/_rhs2523 {
							panic("integer overflow")
						}
					}
				}
				return _p2522 * _rhs2523
			}())
			var _rhs2521 int = digit
			if _rhs2521 > 0 && _cur2520 > math.MaxInt-_rhs2521 {
				panic("integer overflow")
			}
			if _rhs2521 < 0 && _cur2520 < math.MinInt-_rhs2521 {
				panic("integer overflow")
			}
			value = _cur2520 + _rhs2521
		}()
		// Osty: /tmp/selfhost_merged.osty:17831:9
		func() {
			var _cur2524 int = consumed
			var _rhs2525 int = 1
			if _rhs2525 > 0 && _cur2524 > math.MaxInt-_rhs2525 {
				panic("integer overflow")
			}
			if _rhs2525 < 0 && _cur2524 < math.MinInt-_rhs2525 {
				panic("integer overflow")
			}
			consumed = _cur2524 + _rhs2525
		}()
	}
	return &AstLowerUnicodeEscape{value: 0, consumed: 0}
}

// Osty: /tmp/selfhost_merged.osty:17836:1
func astLowerStringUnitCount(units []string) int {
	// Osty: /tmp/selfhost_merged.osty:17837:5
	count := 0
	_ = count
	// Osty: /tmp/selfhost_merged.osty:17838:5
	for _, unit := range units {
		// Osty: /tmp/selfhost_merged.osty:17839:9
		_ = unit
		// Osty: /tmp/selfhost_merged.osty:17840:9
		func() {
			var _cur2526 int = count
			var _rhs2527 int = 1
			if _rhs2527 > 0 && _cur2526 > math.MaxInt-_rhs2527 {
				panic("integer overflow")
			}
			if _rhs2527 < 0 && _cur2526 < math.MinInt-_rhs2527 {
				panic("integer overflow")
			}
			count = _cur2526 + _rhs2527
		}()
	}
	return count
}

// Osty: /tmp/selfhost_merged.osty:17845:1
func astLowerDecl(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Decl {
	// Osty: /tmp/selfhost_merged.osty:17846:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:17847:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNFnDecl{})) {
		// Osty: /tmp/selfhost_merged.osty:17848:9
		return astbridge.FnDeclAsDecl(astLowerFnDecl(arena, toks, n))
	}
	// Osty: /tmp/selfhost_merged.osty:17850:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNStructDecl{})) {
		// Osty: /tmp/selfhost_merged.osty:17851:9
		return astLowerStructDecl(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:17853:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNEnumDecl{})) {
		// Osty: /tmp/selfhost_merged.osty:17854:9
		return astLowerEnumDecl(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:17856:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNInterfaceDecl{})) {
		// Osty: /tmp/selfhost_merged.osty:17857:9
		return astLowerInterfaceDecl(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:17859:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNTypeAlias{})) {
		// Osty: /tmp/selfhost_merged.osty:17860:9
		return astLowerTypeAliasDecl(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:17862:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNUseDecl{})) {
		// Osty: /tmp/selfhost_merged.osty:17863:9
		return astLowerUseDecl(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:17865:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNLet{})) {
		// Osty: /tmp/selfhost_merged.osty:17866:9
		return astLowerLetDecl(arena, toks, n)
	}
	return astbridge.NilDecl()
}

// Osty: /tmp/selfhost_merged.osty:17871:1
func astLowerFnDecl(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.FnDecl {
	// Osty: /tmp/selfhost_merged.osty:17872:5
	recv := astbridge.NilReceiver()
	_ = recv
	// Osty: /tmp/selfhost_merged.osty:17873:5
	params := astbridge.EmptyParamList()
	_ = params
	// Osty: /tmp/selfhost_merged.osty:17874:5
	i := 0
	_ = i
	// Osty: /tmp/selfhost_merged.osty:17875:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:17876:9
		p := astLowerParam(arena, toks, child)
		_ = p
		// Osty: /tmp/selfhost_merged.osty:17877:9
		if !(astbridge.IsNilParam(p)) {
			// Osty: /tmp/selfhost_merged.osty:17878:13
			cn := astArenaNodeAt(arena, child)
			_ = cn
			// Osty: /tmp/selfhost_merged.osty:17879:13
			if i == 0 && cn.text == "self" {
				// Osty: /tmp/selfhost_merged.osty:17880:17
				recv = astbridge.ReceiverNode(astLowerNodePos(toks, cn), astLowerNodeEnd(toks, cn), cn.flags == 1, astLowerNodePos(toks, cn))
			} else {
				// Osty: /tmp/selfhost_merged.osty:17882:17
				func() struct{} { params = append(params, p); return struct{}{} }()
			}
		}
		// Osty: /tmp/selfhost_merged.osty:17885:9
		func() {
			var _cur2528 int = i
			var _rhs2529 int = 1
			if _rhs2529 > 0 && _cur2528 > math.MaxInt-_rhs2529 {
				panic("integer overflow")
			}
			if _rhs2529 < 0 && _cur2528 < math.MinInt-_rhs2529 {
				panic("integer overflow")
			}
			i = _cur2528 + _rhs2529
		}()
	}
	return astbridge.FnDeclNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.flags == 1, n.text, astLowerGenericParams(arena, toks, n.children2), recv, params, astLowerType(arena, toks, n.left), astLowerBlock(arena, toks, n.right), astLowerDoc(toks, n.start), astLowerAnnotations(arena, toks, n.extra))
}

// Osty: /tmp/selfhost_merged.osty:17902:1
func astLowerStructDecl(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Decl {
	// Osty: /tmp/selfhost_merged.osty:17903:5
	fields := astbridge.EmptyFieldList()
	_ = fields
	// Osty: /tmp/selfhost_merged.osty:17904:5
	methods := astbridge.EmptyFnDeclList()
	_ = methods
	// Osty: /tmp/selfhost_merged.osty:17905:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:17906:9
		cn := astArenaNodeAt(arena, child)
		_ = cn
		// Osty: /tmp/selfhost_merged.osty:17907:9
		{
			_m2530 := cn.kind
			_ = _m2530
			if func() bool { _, ok := _m2530.(*AstNodeKind_AstNFnDecl); return ok }() {
				// Osty: /tmp/selfhost_merged.osty:17909:17
				fnDecl := astLowerFnDecl(arena, toks, cn)
				_ = fnDecl
				// Osty: /tmp/selfhost_merged.osty:17910:17
				if !(astbridge.IsNilFnDecl(fnDecl)) {
					// Osty: /tmp/selfhost_merged.osty:17911:21
					func() struct{} { methods = append(methods, fnDecl); return struct{}{} }()
				}
			}
			if func() bool { _, ok := _m2530.(*AstNodeKind_AstNField_); return ok }() {
				// Osty: /tmp/selfhost_merged.osty:17915:17
				field := astLowerField(arena, toks, cn)
				_ = field
				// Osty: /tmp/selfhost_merged.osty:17916:17
				if !(astbridge.IsNilField(field)) {
					// Osty: /tmp/selfhost_merged.osty:17917:21
					func() struct{} { fields = append(fields, field); return struct{}{} }()
				}
			}
			{
			}
		}
	}
	return astbridge.StructDeclNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.flags == 1, n.text, astLowerGenericParams(arena, toks, n.children2), fields, methods, astLowerDoc(toks, n.start), astLowerAnnotations(arena, toks, n.extra))
}

// Osty: /tmp/selfhost_merged.osty:17936:1
func astLowerEnumDecl(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Decl {
	// Osty: /tmp/selfhost_merged.osty:17937:5
	variants := astbridge.EmptyVariantList()
	_ = variants
	// Osty: /tmp/selfhost_merged.osty:17938:5
	methods := astbridge.EmptyFnDeclList()
	_ = methods
	// Osty: /tmp/selfhost_merged.osty:17939:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:17940:9
		cn := astArenaNodeAt(arena, child)
		_ = cn
		// Osty: /tmp/selfhost_merged.osty:17941:9
		{
			_m2531 := cn.kind
			_ = _m2531
			if func() bool { _, ok := _m2531.(*AstNodeKind_AstNFnDecl); return ok }() {
				// Osty: /tmp/selfhost_merged.osty:17943:17
				fnDecl := astLowerFnDecl(arena, toks, cn)
				_ = fnDecl
				// Osty: /tmp/selfhost_merged.osty:17944:17
				if !(astbridge.IsNilFnDecl(fnDecl)) {
					// Osty: /tmp/selfhost_merged.osty:17945:21
					func() struct{} { methods = append(methods, fnDecl); return struct{}{} }()
				}
			}
			if func() bool { _, ok := _m2531.(*AstNodeKind_AstNVariant); return ok }() {
				// Osty: /tmp/selfhost_merged.osty:17949:17
				fields := astbridge.EmptyTypeList()
				_ = fields
				// Osty: /tmp/selfhost_merged.osty:17950:17
				for _, t := range cn.children {
					// Osty: /tmp/selfhost_merged.osty:17951:21
					ty := astLowerType(arena, toks, t)
					_ = ty
					// Osty: /tmp/selfhost_merged.osty:17952:21
					if !(astbridge.IsNilType(ty)) {
						// Osty: /tmp/selfhost_merged.osty:17953:25
						func() struct{} { fields = append(fields, ty); return struct{}{} }()
					}
				}
				// Osty: /tmp/selfhost_merged.osty:17956:17
				func() struct{} {
					variants = append(variants, astbridge.VariantNode(astLowerNodePos(toks, cn), astLowerNodeEnd(toks, cn), cn.text, fields, astLowerAnnotations(arena, toks, cn.extra), astLowerDoc(toks, cn.start)))
					return struct{}{}
				}()
			}
			{
			}
		}
	}
	return astbridge.EnumDeclNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.flags == 1, n.text, astLowerGenericParams(arena, toks, n.children2), variants, methods, astLowerDoc(toks, n.start), astLowerAnnotations(arena, toks, n.extra))
}

// Osty: /tmp/selfhost_merged.osty:17981:1
func astLowerInterfaceDecl(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Decl {
	// Osty: /tmp/selfhost_merged.osty:17982:5
	extends := astbridge.EmptyTypeList()
	_ = extends
	// Osty: /tmp/selfhost_merged.osty:17983:5
	methods := astbridge.EmptyFnDeclList()
	_ = methods
	// Osty: /tmp/selfhost_merged.osty:17984:5
	for _, idx := range n.children2 {
		// Osty: /tmp/selfhost_merged.osty:17985:9
		ty := astLowerType(arena, toks, idx)
		_ = ty
		// Osty: /tmp/selfhost_merged.osty:17986:9
		if !(astbridge.IsNilType(ty)) {
			// Osty: /tmp/selfhost_merged.osty:17987:13
			func() struct{} { extends = append(extends, ty); return struct{}{} }()
		}
	}
	// Osty: /tmp/selfhost_merged.osty:17990:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:17991:9
		cn := astArenaNodeAt(arena, child)
		_ = cn
		// Osty: /tmp/selfhost_merged.osty:17992:9
		if ostyEqual(cn.kind, AstNodeKind(&AstNodeKind_AstNFnDecl{})) {
			// Osty: /tmp/selfhost_merged.osty:17993:13
			fnDecl := astLowerFnDecl(arena, toks, cn)
			_ = fnDecl
			// Osty: /tmp/selfhost_merged.osty:17994:13
			if !(astbridge.IsNilFnDecl(fnDecl)) {
				// Osty: /tmp/selfhost_merged.osty:17995:17
				func() struct{} { methods = append(methods, fnDecl); return struct{}{} }()
			}
		} else {
			// Osty: /tmp/selfhost_merged.osty:17998:13
			ty := astLowerType(arena, toks, child)
			_ = ty
			// Osty: /tmp/selfhost_merged.osty:17999:13
			if !(astbridge.IsNilType(ty)) {
				// Osty: /tmp/selfhost_merged.osty:18000:17
				func() struct{} { extends = append(extends, ty); return struct{}{} }()
			}
		}
	}
	return astbridge.InterfaceDeclNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.flags == 1, n.text, astbridge.EmptyGenericParamList(), extends, methods, astLowerDoc(toks, n.start), astLowerAnnotations(arena, toks, n.extra))
}

// Osty: /tmp/selfhost_merged.osty:18017:1
func astLowerTypeAliasDecl(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Decl {
	return astbridge.TypeAliasDeclNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.flags == 1, n.text, astLowerGenericParams(arena, toks, n.children), astLowerType(arena, toks, n.left), astLowerDoc(toks, n.start), astLowerAnnotations(arena, toks, n.extra))
}

// Osty: /tmp/selfhost_merged.osty:18030:1
func astLowerUseDecl(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Decl {
	// Osty: /tmp/selfhost_merged.osty:18031:5
	raw := astLowerUnquoteMaybe(n.text)
	_ = raw
	// Osty: /tmp/selfhost_merged.osty:18032:5
	if n.flags == 1 {
		// Osty: /tmp/selfhost_merged.osty:18033:9
		reconstructed := astLowerUseGoRawPath(toks, n)
		_ = reconstructed
		// Osty: /tmp/selfhost_merged.osty:18034:9
		if reconstructed != "" {
			// Osty: /tmp/selfhost_merged.osty:18035:13
			raw = reconstructed
		}
	} else {
		// Osty: /tmp/selfhost_merged.osty:18038:9
		reconstructed := astLowerUseRawPath(toks, n)
		_ = reconstructed
		// Osty: /tmp/selfhost_merged.osty:18039:9
		if reconstructed != "" {
			// Osty: /tmp/selfhost_merged.osty:18040:13
			raw = reconstructed
		}
	}
	// Osty: /tmp/selfhost_merged.osty:18043:5
	alias := ""
	_ = alias
	// Osty: /tmp/selfhost_merged.osty:18044:5
	if astLowerIntListCount(n.children2) > 0 {
		// Osty: /tmp/selfhost_merged.osty:18045:9
		aliasNode := astArenaNodeAt(arena, astLowerIntListAt(n.children2, 0))
		_ = aliasNode
		// Osty: /tmp/selfhost_merged.osty:18046:9
		alias = aliasNode.text
	}
	// Osty: /tmp/selfhost_merged.osty:18048:5
	body := astbridge.EmptyDeclList()
	_ = body
	// Osty: /tmp/selfhost_merged.osty:18049:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18050:9
		d := astLowerDecl(arena, toks, child)
		_ = d
		// Osty: /tmp/selfhost_merged.osty:18051:9
		if !(astbridge.IsNilDecl(d)) {
			// Osty: /tmp/selfhost_merged.osty:18052:13
			func() struct{} { body = append(body, d); return struct{}{} }()
		}
	}
	// Osty: /tmp/selfhost_merged.osty:18055:5
	var path []string = make([]string, 0, 1)
	_ = path
	// Osty: /tmp/selfhost_merged.osty:18056:5
	if !(n.flags == 1) {
		// Osty: /tmp/selfhost_merged.osty:18057:9
		path = astLowerSplitPath(raw)
	}
	return astbridge.UseDeclNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), raw, path, n.flags == 1, alias, body)
}

// Osty: /tmp/selfhost_merged.osty:18062:1
func astLowerUseRawPath(toks []astbridge.Token, n *AstNode) string {
	// Osty: /tmp/selfhost_merged.osty:18063:5
	out := ""
	_ = out
	// Osty: /tmp/selfhost_merged.osty:18064:5
	i := func() int {
		var _p2532 int = n.start
		var _rhs2533 int = 1
		if _rhs2533 > 0 && _p2532 > math.MaxInt-_rhs2533 {
			panic("integer overflow")
		}
		if _rhs2533 < 0 && _p2532 < math.MinInt-_rhs2533 {
			panic("integer overflow")
		}
		return _p2532 + _rhs2533
	}()
	_ = i
	// Osty: /tmp/selfhost_merged.osty:18065:5
	for i < n.end {
		// Osty: /tmp/selfhost_merged.osty:18066:9
		tok := astLowerTok(toks, i)
		_ = tok
		// Osty: /tmp/selfhost_merged.osty:18067:9
		if astbridge.TokenIsIdent(tok) {
			// Osty: /tmp/selfhost_merged.osty:18068:13
			if astbridge.TokenValue(tok) == "as" {
				// Osty: /tmp/selfhost_merged.osty:18069:17
				return out
			}
			// Osty: /tmp/selfhost_merged.osty:18071:13
			out = fmt.Sprintf("%s%s", ostyToString(out), ostyToString(astbridge.TokenValue(tok)))
		} else if astbridge.TokenIsDot(tok) || astbridge.TokenIsSlash(tok) || astbridge.TokenIsColon(tok) {
			// Osty: /tmp/selfhost_merged.osty:18073:13
			out = fmt.Sprintf("%s%s", ostyToString(out), ostyToString(astbridge.TokenKindString(tok)))
		} else {
			// Osty: /tmp/selfhost_merged.osty:18075:13
			return out
		}
		// Osty: /tmp/selfhost_merged.osty:18077:9
		func() {
			var _cur2534 int = i
			var _rhs2535 int = 1
			if _rhs2535 > 0 && _cur2534 > math.MaxInt-_rhs2535 {
				panic("integer overflow")
			}
			if _rhs2535 < 0 && _cur2534 < math.MinInt-_rhs2535 {
				panic("integer overflow")
			}
			i = _cur2534 + _rhs2535
		}()
	}
	return out
}

// Osty: /tmp/selfhost_merged.osty:18082:1
func astLowerUseGoRawPath(toks []astbridge.Token, n *AstNode) string {
	// Osty: /tmp/selfhost_merged.osty:18083:5
	i := func() int {
		var _p2536 int = n.start
		var _rhs2537 int = 1
		if _rhs2537 > 0 && _p2536 > math.MaxInt-_rhs2537 {
			panic("integer overflow")
		}
		if _rhs2537 < 0 && _p2536 < math.MinInt-_rhs2537 {
			panic("integer overflow")
		}
		return _p2536 + _rhs2537
	}()
	_ = i
	// Osty: /tmp/selfhost_merged.osty:18084:5
	for i < n.end {
		// Osty: /tmp/selfhost_merged.osty:18085:9
		tok := astLowerTok(toks, i)
		_ = tok
		// Osty: /tmp/selfhost_merged.osty:18086:9
		if astbridge.TokenIsString(tok) {
			// Osty: /tmp/selfhost_merged.osty:18087:13
			return astLowerUnquoteMaybe(astbridge.TokenValue(tok))
		}
		// Osty: /tmp/selfhost_merged.osty:18089:9
		if astbridge.TokenIsLBrace(tok) || astbridge.TokenIsNewline(tok) || astbridge.TokenIsEOF(tok) {
			// Osty: /tmp/selfhost_merged.osty:18090:13
			return ""
		}
		// Osty: /tmp/selfhost_merged.osty:18092:9
		func() {
			var _cur2538 int = i
			var _rhs2539 int = 1
			if _rhs2539 > 0 && _cur2538 > math.MaxInt-_rhs2539 {
				panic("integer overflow")
			}
			if _rhs2539 < 0 && _cur2538 < math.MinInt-_rhs2539 {
				panic("integer overflow")
			}
			i = _cur2538 + _rhs2539
		}()
	}
	return ""
}

// Osty: /tmp/selfhost_merged.osty:18097:1
func astLowerLetDecl(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Decl {
	// Osty: /tmp/selfhost_merged.osty:18098:5
	name := ""
	_ = name
	// Osty: /tmp/selfhost_merged.osty:18099:5
	patNode := astArenaNodeAt(arena, n.left)
	_ = patNode
	// Osty: /tmp/selfhost_merged.osty:18100:5
	if ostyEqual(patNode.kind, AstNodeKind(&AstNodeKind_AstNIdent{})) {
		// Osty: /tmp/selfhost_merged.osty:18101:9
		name = patNode.text
	} else if ostyEqual(patNode.kind, AstNodeKind(&AstNodeKind_AstNPattern{})) && patNode.extra == astPatternIdentKind() {
		// Osty: /tmp/selfhost_merged.osty:18103:9
		name = patNode.text
	} else if strings.HasPrefix(patNode.text, "ident:") {
		// Osty: /tmp/selfhost_merged.osty:18105:9
		name = strings.TrimPrefix(patNode.text, "ident:")
	}
	return astbridge.LetDeclNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astbridge.TokenIsPub(astLowerTok(toks, func() int {
		var _p2540 int = n.start
		var _rhs2541 int = 1
		if _rhs2541 < 0 && _p2540 > math.MaxInt+_rhs2541 {
			panic("integer overflow")
		}
		if _rhs2541 > 0 && _p2540 < math.MinInt+_rhs2541 {
			panic("integer overflow")
		}
		return _p2540 - _rhs2541
	}())), n.flags == 1, astLowerMutPos(toks, n), name, astLowerChildType(arena, toks, n, 0), astLowerExpr(arena, toks, n.right), astLowerDoc(toks, n.start), astLowerAnnotations(arena, toks, n.extra))
}

// Osty: /tmp/selfhost_merged.osty:18121:1
func astLowerField(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Field {
	return astbridge.FieldNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.flags == 1, n.text, astLowerType(arena, toks, n.right), astLowerExpr(arena, toks, n.left), astLowerDoc(toks, n.start), astLowerAnnotations(arena, toks, n.extra))
}

// Osty: /tmp/selfhost_merged.osty:18134:1
func astLowerParam(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Param {
	// Osty: /tmp/selfhost_merged.osty:18135:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18136:9
		return astbridge.NilParam()
	}
	// Osty: /tmp/selfhost_merged.osty:18138:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18139:5
	pat := astbridge.NilPattern()
	_ = pat
	// Osty: /tmp/selfhost_merged.osty:18140:5
	def := astbridge.NilExpr()
	_ = def
	// Osty: /tmp/selfhost_merged.osty:18141:5
	if n.left >= 0 {
		// Osty: /tmp/selfhost_merged.osty:18142:9
		if n.text == "" {
			// Osty: /tmp/selfhost_merged.osty:18143:13
			parsedPat := astLowerPattern(arena, toks, n.left)
			_ = parsedPat
			// Osty: /tmp/selfhost_merged.osty:18144:13
			if !(astbridge.IsNilPattern(parsedPat)) {
				// Osty: /tmp/selfhost_merged.osty:18145:17
				pat = parsedPat
			}
		} else {
			// Osty: /tmp/selfhost_merged.osty:18148:13
			def = astLowerExpr(arena, toks, n.left)
		}
	}
	return astbridge.ParamNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.text, pat, astLowerType(arena, toks, n.right), def)
}

// Osty: /tmp/selfhost_merged.osty:18154:1
func astLowerGenericParams(arena *AstArena, toks []astbridge.Token, ids []int) []astbridge.GenericParam {
	// Osty: /tmp/selfhost_merged.osty:18155:5
	out := astbridge.EmptyGenericParamList()
	_ = out
	// Osty: /tmp/selfhost_merged.osty:18156:5
	for _, idx := range ids {
		// Osty: /tmp/selfhost_merged.osty:18157:9
		n := astArenaNodeAt(arena, idx)
		_ = n
		// Osty: /tmp/selfhost_merged.osty:18158:9
		constraints := astbridge.EmptyTypeList()
		_ = constraints
		// Osty: /tmp/selfhost_merged.osty:18159:9
		for _, child := range n.children {
			// Osty: /tmp/selfhost_merged.osty:18160:13
			ty := astLowerType(arena, toks, child)
			_ = ty
			// Osty: /tmp/selfhost_merged.osty:18161:13
			if !(astbridge.IsNilType(ty)) {
				// Osty: /tmp/selfhost_merged.osty:18162:17
				func() struct{} { constraints = append(constraints, ty); return struct{}{} }()
			}
		}
		// Osty: /tmp/selfhost_merged.osty:18165:9
		func() struct{} {
			out = append(out, astbridge.GenericParamNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.text, constraints))
			return struct{}{}
		}()
	}
	return out
}

// Osty: /tmp/selfhost_merged.osty:18170:1
func astLowerAnnotations(arena *AstArena, toks []astbridge.Token, idx int) []astbridge.Annotation {
	// Osty: /tmp/selfhost_merged.osty:18171:5
	out := astbridge.EmptyAnnotationList()
	_ = out
	// Osty: /tmp/selfhost_merged.osty:18172:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18173:9
		return out
	}
	// Osty: /tmp/selfhost_merged.osty:18175:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18176:5
	if n.text == "__group" {
		// Osty: /tmp/selfhost_merged.osty:18177:9
		for _, child := range n.children {
			// Osty: /tmp/selfhost_merged.osty:18178:13
			ann := astLowerAnnotation(arena, toks, child)
			_ = ann
			// Osty: /tmp/selfhost_merged.osty:18179:13
			if !(astbridge.IsNilAnnotation(ann)) {
				// Osty: /tmp/selfhost_merged.osty:18180:17
				func() struct{} { out = append(out, ann); return struct{}{} }()
			}
		}
		// Osty: /tmp/selfhost_merged.osty:18183:9
		return out
	}
	// Osty: /tmp/selfhost_merged.osty:18185:5
	ann := astLowerAnnotation(arena, toks, idx)
	_ = ann
	// Osty: /tmp/selfhost_merged.osty:18186:5
	if !(astbridge.IsNilAnnotation(ann)) {
		// Osty: /tmp/selfhost_merged.osty:18187:9
		func() struct{} { out = append(out, ann); return struct{}{} }()
	}
	return out
}

// Osty: /tmp/selfhost_merged.osty:18192:1
func astLowerAnnotation(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Annotation {
	// Osty: /tmp/selfhost_merged.osty:18193:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18194:9
		return astbridge.NilAnnotation()
	}
	// Osty: /tmp/selfhost_merged.osty:18196:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18197:5
	args := astbridge.EmptyAnnotationArgList()
	_ = args
	// Osty: /tmp/selfhost_merged.osty:18198:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18199:9
		cn := astArenaNodeAt(arena, child)
		_ = cn
		// Osty: /tmp/selfhost_merged.osty:18200:9
		if ostyEqual(cn.kind, AstNodeKind(&AstNodeKind_AstNField_{})) {
			// Osty: /tmp/selfhost_merged.osty:18201:13
			func() struct{} {
				args = append(args, astbridge.AnnotationArgNode(astLowerNodePos(toks, cn), cn.text, astLowerExpr(arena, toks, cn.left)))
				return struct{}{}
			}()
		} else if ostyEqual(cn.kind, AstNodeKind(&AstNodeKind_AstNIdent{})) {
			// Osty: /tmp/selfhost_merged.osty:18203:13
			func() struct{} {
				args = append(args, astbridge.AnnotationArgNode(astLowerNodePos(toks, cn), cn.text, astbridge.NilExpr()))
				return struct{}{}
			}()
		} else {
			// Osty: /tmp/selfhost_merged.osty:18205:13
			func() struct{} {
				args = append(args, astbridge.AnnotationArgNode(astLowerNodePos(toks, cn), "", astLowerExpr(arena, toks, child)))
				return struct{}{}
			}()
		}
	}
	return astbridge.AnnotationNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.text, args)
}

// Osty: /tmp/selfhost_merged.osty:18211:1
func astLowerType(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Type {
	// Osty: /tmp/selfhost_merged.osty:18212:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18213:9
		return astbridge.NilType()
	}
	// Osty: /tmp/selfhost_merged.osty:18215:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18216:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNError{})) {
		// Osty: /tmp/selfhost_merged.osty:18217:9
		return astbridge.NilType()
	}
	// Osty: /tmp/selfhost_merged.osty:18219:5
	if n.text == "optional" {
		// Osty: /tmp/selfhost_merged.osty:18220:9
		return astbridge.OptionalTypeNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerType(arena, toks, n.left))
	}
	// Osty: /tmp/selfhost_merged.osty:18222:5
	if n.text == "tuple" {
		// Osty: /tmp/selfhost_merged.osty:18223:9
		elems := astbridge.EmptyTypeList()
		_ = elems
		// Osty: /tmp/selfhost_merged.osty:18224:9
		for _, child := range n.children {
			// Osty: /tmp/selfhost_merged.osty:18225:13
			ty := astLowerType(arena, toks, child)
			_ = ty
			// Osty: /tmp/selfhost_merged.osty:18226:13
			if !(astbridge.IsNilType(ty)) {
				// Osty: /tmp/selfhost_merged.osty:18227:17
				func() struct{} { elems = append(elems, ty); return struct{}{} }()
			}
		}
		// Osty: /tmp/selfhost_merged.osty:18230:9
		return astbridge.TupleTypeNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), elems)
	}
	// Osty: /tmp/selfhost_merged.osty:18232:5
	if n.text == "fn" {
		// Osty: /tmp/selfhost_merged.osty:18233:9
		params := astbridge.EmptyTypeList()
		_ = params
		// Osty: /tmp/selfhost_merged.osty:18234:9
		for _, child := range n.children {
			// Osty: /tmp/selfhost_merged.osty:18235:13
			ty := astLowerType(arena, toks, child)
			_ = ty
			// Osty: /tmp/selfhost_merged.osty:18236:13
			if !(astbridge.IsNilType(ty)) {
				// Osty: /tmp/selfhost_merged.osty:18237:17
				func() struct{} { params = append(params, ty); return struct{}{} }()
			}
		}
		// Osty: /tmp/selfhost_merged.osty:18240:9
		return astbridge.FnTypeNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), params, astLowerType(arena, toks, n.right))
	}
	// Osty: /tmp/selfhost_merged.osty:18242:5
	args := astbridge.EmptyTypeList()
	_ = args
	// Osty: /tmp/selfhost_merged.osty:18243:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18244:9
		ty := astLowerType(arena, toks, child)
		_ = ty
		// Osty: /tmp/selfhost_merged.osty:18245:9
		if !(astbridge.IsNilType(ty)) {
			// Osty: /tmp/selfhost_merged.osty:18246:13
			func() struct{} { args = append(args, ty); return struct{}{} }()
		}
	}
	return astbridge.NamedTypeNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerSplitPath(n.text), args)
}

// Osty: /tmp/selfhost_merged.osty:18252:1
func astLowerChildType(arena *AstArena, toks []astbridge.Token, n *AstNode, at int) astbridge.Type {
	// Osty: /tmp/selfhost_merged.osty:18253:5
	if at < 0 || at >= astLowerIntListCount(n.children) {
		// Osty: /tmp/selfhost_merged.osty:18254:9
		return astbridge.NilType()
	}
	return astLowerType(arena, toks, astLowerIntListAt(n.children, at))
}

// Osty: /tmp/selfhost_merged.osty:18259:1
func astLowerStmt(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Stmt {
	// Osty: /tmp/selfhost_merged.osty:18260:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18261:9
		return astbridge.NilStmt()
	}
	// Osty: /tmp/selfhost_merged.osty:18263:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18264:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNLet{})) {
		// Osty: /tmp/selfhost_merged.osty:18265:9
		return astbridge.LetStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerPattern(arena, toks, n.left), n.flags == 1, astLowerMutPos(toks, n), astLowerChildType(arena, toks, n, 0), astLowerExpr(arena, toks, n.right))
	}
	// Osty: /tmp/selfhost_merged.osty:18267:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNReturn{})) {
		// Osty: /tmp/selfhost_merged.osty:18268:9
		return astbridge.ReturnStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerExpr(arena, toks, n.left))
	}
	// Osty: /tmp/selfhost_merged.osty:18270:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNBreak{})) {
		// Osty: /tmp/selfhost_merged.osty:18271:9
		return astbridge.BreakStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n))
	}
	// Osty: /tmp/selfhost_merged.osty:18273:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNContinue{})) {
		// Osty: /tmp/selfhost_merged.osty:18274:9
		return astbridge.ContinueStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n))
	}
	// Osty: /tmp/selfhost_merged.osty:18276:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNDefer{})) {
		// Osty: /tmp/selfhost_merged.osty:18277:9
		return astbridge.DeferStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerExpr(arena, toks, n.left))
	}
	// Osty: /tmp/selfhost_merged.osty:18279:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNFor{})) {
		// Osty: /tmp/selfhost_merged.osty:18280:9
		return astLowerForStmt(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18282:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNAssign{})) {
		// Osty: /tmp/selfhost_merged.osty:18283:9
		return astbridge.AssignStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerKind(n.op), astLowerExpr(arena, toks, n.left), astLowerExpr(arena, toks, n.right))
	}
	// Osty: /tmp/selfhost_merged.osty:18285:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNChanSend{})) {
		// Osty: /tmp/selfhost_merged.osty:18286:9
		return astbridge.ChanSendStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerExpr(arena, toks, n.left), astLowerExpr(arena, toks, n.right))
	}
	// Osty: /tmp/selfhost_merged.osty:18288:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNExprStmt{})) {
		// Osty: /tmp/selfhost_merged.osty:18289:9
		return astbridge.ExprStmtNode(astLowerExpr(arena, toks, n.left))
	}
	// Osty: /tmp/selfhost_merged.osty:18291:5
	e := astLowerExpr(arena, toks, idx)
	_ = e
	// Osty: /tmp/selfhost_merged.osty:18292:5
	if !(astbridge.IsNilExpr(e)) {
		// Osty: /tmp/selfhost_merged.osty:18293:9
		return astbridge.ExprStmtNode(e)
	}
	return astbridge.NilStmt()
}

// Osty: /tmp/selfhost_merged.osty:18298:1
func astLowerForStmt(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Stmt {
	// Osty: /tmp/selfhost_merged.osty:18299:5
	if n.text == "forlet" {
		// Osty: /tmp/selfhost_merged.osty:18300:9
		return astbridge.ForStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), true, astLowerChildPattern(arena, toks, n, 0), astLowerExpr(arena, toks, n.left), astLowerBlock(arena, toks, n.right))
	}
	// Osty: /tmp/selfhost_merged.osty:18309:5
	if n.text == "forin" {
		// Osty: /tmp/selfhost_merged.osty:18310:9
		return astbridge.ForStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), false, astLowerChildPattern(arena, toks, n, 0), astLowerChildExpr(arena, toks, n, 1), astLowerBlock(arena, toks, n.right))
	}
	return astbridge.ForStmtNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), false, astbridge.NilPattern(), astLowerExpr(arena, toks, n.left), astLowerBlock(arena, toks, n.right))
}

// Osty: /tmp/selfhost_merged.osty:18329:1
func astLowerBlock(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Block {
	// Osty: /tmp/selfhost_merged.osty:18330:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18331:9
		return astbridge.NilBlock()
	}
	// Osty: /tmp/selfhost_merged.osty:18333:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18334:5
	stmts := astbridge.EmptyStmtList()
	_ = stmts
	// Osty: /tmp/selfhost_merged.osty:18335:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18336:9
		stmt := astLowerStmt(arena, toks, child)
		_ = stmt
		// Osty: /tmp/selfhost_merged.osty:18337:9
		if !(astbridge.IsNilStmt(stmt)) {
			// Osty: /tmp/selfhost_merged.osty:18338:13
			func() struct{} { stmts = append(stmts, stmt); return struct{}{} }()
		}
	}
	return astbridge.BlockNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), stmts)
}

// Osty: /tmp/selfhost_merged.osty:18344:1
func astLowerExpr(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18345:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18346:9
		return astbridge.NilExpr()
	}
	// Osty: /tmp/selfhost_merged.osty:18348:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18349:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNIdent{})) {
		// Osty: /tmp/selfhost_merged.osty:18350:9
		return astbridge.IdentExpr(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.text)
	}
	// Osty: /tmp/selfhost_merged.osty:18352:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNIntLit{})) {
		// Osty: /tmp/selfhost_merged.osty:18353:9
		return astbridge.IntLitExpr(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.text)
	}
	// Osty: /tmp/selfhost_merged.osty:18355:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNFloatLit{})) {
		// Osty: /tmp/selfhost_merged.osty:18356:9
		return astbridge.FloatLitExpr(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.text)
	}
	// Osty: /tmp/selfhost_merged.osty:18358:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNBoolLit{})) {
		// Osty: /tmp/selfhost_merged.osty:18359:9
		return astbridge.BoolLitExpr(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.flags == 1)
	}
	// Osty: /tmp/selfhost_merged.osty:18361:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNCharLit{})) {
		// Osty: /tmp/selfhost_merged.osty:18362:9
		return astbridge.CharLitExpr(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerDecodedLiteral(astbridge.TokenValue(astLowerTok(toks, n.start))))
	}
	// Osty: /tmp/selfhost_merged.osty:18364:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNByteLit{})) {
		// Osty: /tmp/selfhost_merged.osty:18365:9
		return astbridge.ByteLitExpr(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerDecodedLiteral(astbridge.TokenValue(astLowerTok(toks, n.start))))
	}
	// Osty: /tmp/selfhost_merged.osty:18367:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNStringLit{})) {
		// Osty: /tmp/selfhost_merged.osty:18368:9
		return astbridge.StringLitFromToken(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerTok(toks, n.start))
	}
	// Osty: /tmp/selfhost_merged.osty:18370:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNUnary{})) {
		// Osty: /tmp/selfhost_merged.osty:18371:9
		return astLowerUnaryExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18373:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNBinary{})) {
		// Osty: /tmp/selfhost_merged.osty:18374:9
		return astLowerBinaryExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18376:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNQuestion{})) {
		// Osty: /tmp/selfhost_merged.osty:18377:9
		return astLowerQuestionExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18379:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNCall{})) {
		// Osty: /tmp/selfhost_merged.osty:18380:9
		return astLowerCallExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18382:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNField{})) {
		// Osty: /tmp/selfhost_merged.osty:18383:9
		return astLowerFieldExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18385:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNIndex{})) {
		// Osty: /tmp/selfhost_merged.osty:18386:9
		return astLowerIndexExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18388:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNTurbofish{})) {
		// Osty: /tmp/selfhost_merged.osty:18389:9
		return astLowerTurbofishExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18391:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNRange{})) {
		// Osty: /tmp/selfhost_merged.osty:18392:9
		return astLowerRangeExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18394:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNParen{})) {
		// Osty: /tmp/selfhost_merged.osty:18395:9
		return astbridge.ParenExprNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerExpr(arena, toks, n.left))
	}
	// Osty: /tmp/selfhost_merged.osty:18397:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNTuple{})) {
		// Osty: /tmp/selfhost_merged.osty:18398:9
		return astLowerTupleExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18400:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNList{})) {
		// Osty: /tmp/selfhost_merged.osty:18401:9
		return astLowerListExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18403:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNMap{})) {
		// Osty: /tmp/selfhost_merged.osty:18404:9
		return astLowerMapExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18406:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNStructLit{})) {
		// Osty: /tmp/selfhost_merged.osty:18407:9
		return astLowerStructLitExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18409:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNBlock{})) {
		// Osty: /tmp/selfhost_merged.osty:18410:9
		return astbridge.BlockAsExpr(astLowerBlock(arena, toks, idx))
	}
	// Osty: /tmp/selfhost_merged.osty:18412:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNIf{})) {
		// Osty: /tmp/selfhost_merged.osty:18413:9
		return astLowerIfExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18415:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNMatch{})) {
		// Osty: /tmp/selfhost_merged.osty:18416:9
		return astLowerMatchExpr(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18418:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNClosure{})) {
		// Osty: /tmp/selfhost_merged.osty:18419:9
		return astLowerClosureExpr(arena, toks, n)
	}
	return astbridge.NilExpr()
}

// Osty: /tmp/selfhost_merged.osty:18424:1
func astLowerUnaryExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18425:5
	x := astLowerExpr(arena, toks, n.left)
	_ = x
	return astbridge.UnaryExprNode(astLowerNodePos(toks, n), astbridge.ExprEnd(x, astLowerNodeEnd(toks, n)), astLowerKind(n.op), x)
}

// Osty: /tmp/selfhost_merged.osty:18429:1
func astLowerBinaryExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18430:5
	left := astLowerExpr(arena, toks, n.left)
	_ = left
	// Osty: /tmp/selfhost_merged.osty:18431:5
	right := astLowerExpr(arena, toks, n.right)
	_ = right
	return astbridge.BinaryExprNode(astbridge.ExprPos(left, astLowerNodePos(toks, n)), astbridge.ExprEnd(right, astLowerNodeEnd(toks, n)), astLowerKind(n.op), left, right)
}

// Osty: /tmp/selfhost_merged.osty:18435:1
func astLowerQuestionExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18436:5
	x := astLowerExpr(arena, toks, n.left)
	_ = x
	return astbridge.QuestionExprNode(astbridge.ExprPos(x, astLowerNodePos(toks, n)), astLowerNodeEnd(toks, n), x)
}

// Osty: /tmp/selfhost_merged.osty:18440:1
func astLowerCallExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18441:5
	fnExpr := astLowerExpr(arena, toks, n.left)
	_ = fnExpr
	// Osty: /tmp/selfhost_merged.osty:18442:5
	args := astbridge.EmptyArgList()
	_ = args
	// Osty: /tmp/selfhost_merged.osty:18443:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18444:9
		arg := astLowerArg(arena, toks, child)
		_ = arg
		// Osty: /tmp/selfhost_merged.osty:18445:9
		if !(astbridge.IsNilArg(arg)) {
			// Osty: /tmp/selfhost_merged.osty:18446:13
			func() struct{} { args = append(args, arg); return struct{}{} }()
		}
	}
	return astbridge.CallExprNode(astbridge.ExprPos(fnExpr, astLowerNodePos(toks, n)), astLowerNodeEnd(toks, n), fnExpr, args)
}

// Osty: /tmp/selfhost_merged.osty:18452:1
func astLowerFieldExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18453:5
	x := astLowerExpr(arena, toks, n.left)
	_ = x
	return astbridge.FieldExprNode(astbridge.ExprPos(x, astLowerNodePos(toks, n)), astLowerNodeEnd(toks, n), x, n.text, n.flags == 1)
}

// Osty: /tmp/selfhost_merged.osty:18457:1
func astLowerIndexExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18458:5
	x := astLowerExpr(arena, toks, n.left)
	_ = x
	return astbridge.IndexExprNode(astbridge.ExprPos(x, astLowerNodePos(toks, n)), astLowerNodeEnd(toks, n), x, astLowerExpr(arena, toks, n.right))
}

// Osty: /tmp/selfhost_merged.osty:18462:1
func astLowerTurbofishExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18463:5
	base := astLowerExpr(arena, toks, n.left)
	_ = base
	// Osty: /tmp/selfhost_merged.osty:18464:5
	args := astbridge.EmptyTypeList()
	_ = args
	// Osty: /tmp/selfhost_merged.osty:18465:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18466:9
		ty := astLowerType(arena, toks, child)
		_ = ty
		// Osty: /tmp/selfhost_merged.osty:18467:9
		if !(astbridge.IsNilType(ty)) {
			// Osty: /tmp/selfhost_merged.osty:18468:13
			func() struct{} { args = append(args, ty); return struct{}{} }()
		}
	}
	return astbridge.TurbofishExprNode(astbridge.ExprPos(base, astLowerNodePos(toks, n)), astLowerNodeEnd(toks, n), base, args)
}

// Osty: /tmp/selfhost_merged.osty:18474:1
func astLowerRangeExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18475:5
	start := astLowerExpr(arena, toks, n.left)
	_ = start
	// Osty: /tmp/selfhost_merged.osty:18476:5
	stop := astLowerExpr(arena, toks, n.right)
	_ = stop
	return astbridge.RangeExprNode(astbridge.ExprPos(start, astLowerNodePos(toks, n)), astbridge.ExprEnd(stop, astLowerNodeEnd(toks, n)), start, stop, n.flags == 1)
}

// Osty: /tmp/selfhost_merged.osty:18480:1
func astLowerTupleExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18481:5
	elems := astbridge.EmptyExprList()
	_ = elems
	// Osty: /tmp/selfhost_merged.osty:18482:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18483:9
		e := astLowerExpr(arena, toks, child)
		_ = e
		// Osty: /tmp/selfhost_merged.osty:18484:9
		if !(astbridge.IsNilExpr(e)) {
			// Osty: /tmp/selfhost_merged.osty:18485:13
			func() struct{} { elems = append(elems, e); return struct{}{} }()
		}
	}
	return astbridge.TupleExprNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), elems)
}

// Osty: /tmp/selfhost_merged.osty:18491:1
func astLowerListExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18492:5
	elems := astbridge.EmptyExprList()
	_ = elems
	// Osty: /tmp/selfhost_merged.osty:18493:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18494:9
		e := astLowerExpr(arena, toks, child)
		_ = e
		// Osty: /tmp/selfhost_merged.osty:18495:9
		if !(astbridge.IsNilExpr(e)) {
			// Osty: /tmp/selfhost_merged.osty:18496:13
			func() struct{} { elems = append(elems, e); return struct{}{} }()
		}
	}
	return astbridge.ListExprNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), elems)
}

// Osty: /tmp/selfhost_merged.osty:18502:1
func astLowerMapExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18503:5
	entries := astbridge.EmptyMapEntryList()
	_ = entries
	// Osty: /tmp/selfhost_merged.osty:18504:5
	i := 0
	_ = i
	// Osty: /tmp/selfhost_merged.osty:18505:5
	for _, keyIdx := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18506:9
		value := astbridge.NilExpr()
		_ = value
		// Osty: /tmp/selfhost_merged.osty:18507:9
		if i < astLowerIntListCount(n.children2) {
			// Osty: /tmp/selfhost_merged.osty:18508:13
			value = astLowerExpr(arena, toks, astLowerIntListAt(n.children2, i))
		}
		// Osty: /tmp/selfhost_merged.osty:18510:9
		func() struct{} {
			entries = append(entries, astbridge.MapEntryNode(astLowerExpr(arena, toks, keyIdx), value))
			return struct{}{}
		}()
		// Osty: /tmp/selfhost_merged.osty:18511:9
		func() {
			var _cur2542 int = i
			var _rhs2543 int = 1
			if _rhs2543 > 0 && _cur2542 > math.MaxInt-_rhs2543 {
				panic("integer overflow")
			}
			if _rhs2543 < 0 && _cur2542 < math.MinInt-_rhs2543 {
				panic("integer overflow")
			}
			i = _cur2542 + _rhs2543
		}()
	}
	return astbridge.MapExprNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), entries, n.flags == 1)
}

// Osty: /tmp/selfhost_merged.osty:18516:1
func astLowerStructLitExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18517:5
	typ := astLowerExpr(arena, toks, n.left)
	_ = typ
	// Osty: /tmp/selfhost_merged.osty:18518:5
	fields := astbridge.EmptyStructLitFieldList()
	_ = fields
	// Osty: /tmp/selfhost_merged.osty:18519:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18520:9
		cn := astArenaNodeAt(arena, child)
		_ = cn
		// Osty: /tmp/selfhost_merged.osty:18521:9
		func() struct{} {
			fields = append(fields, astbridge.StructLitFieldNode(astLowerNodePos(toks, cn), cn.text, astLowerExpr(arena, toks, cn.left)))
			return struct{}{}
		}()
	}
	return astbridge.StructLitNode(astbridge.ExprPos(typ, astLowerNodePos(toks, n)), astLowerNodeEnd(toks, n), typ, fields, astLowerExpr(arena, toks, n.right))
}

// Osty: /tmp/selfhost_merged.osty:18526:1
func astLowerIfExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18527:5
	alt := astbridge.NilExpr()
	_ = alt
	// Osty: /tmp/selfhost_merged.osty:18528:5
	pat := astbridge.NilPattern()
	_ = pat
	// Osty: /tmp/selfhost_merged.osty:18529:5
	if astLowerIntListCount(n.children) > 0 {
		// Osty: /tmp/selfhost_merged.osty:18530:9
		alt = astLowerExpr(arena, toks, astLowerIntListAt(n.children, 0))
	}
	// Osty: /tmp/selfhost_merged.osty:18532:5
	if astLowerIntListCount(n.children) > 1 {
		// Osty: /tmp/selfhost_merged.osty:18533:9
		pat = astLowerPattern(arena, toks, astLowerIntListAt(n.children, 1))
	}
	return astbridge.IfExprNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.flags == 1, pat, astLowerExpr(arena, toks, n.left), astLowerBlock(arena, toks, n.right), alt)
}

// Osty: /tmp/selfhost_merged.osty:18538:1
func astLowerMatchExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18539:5
	arms := astbridge.EmptyMatchArmList()
	_ = arms
	// Osty: /tmp/selfhost_merged.osty:18540:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18541:9
		arm := astLowerMatchArm(arena, toks, child)
		_ = arm
		// Osty: /tmp/selfhost_merged.osty:18542:9
		if !(astbridge.IsNilMatchArm(arm)) {
			// Osty: /tmp/selfhost_merged.osty:18543:13
			func() struct{} { arms = append(arms, arm); return struct{}{} }()
		}
	}
	return astbridge.MatchExprNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerExpr(arena, toks, n.left), arms)
}

// Osty: /tmp/selfhost_merged.osty:18549:1
func astLowerClosureExpr(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18550:5
	params := astbridge.EmptyParamList()
	_ = params
	// Osty: /tmp/selfhost_merged.osty:18551:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18552:9
		p := astLowerParam(arena, toks, child)
		_ = p
		// Osty: /tmp/selfhost_merged.osty:18553:9
		if !(astbridge.IsNilParam(p)) {
			// Osty: /tmp/selfhost_merged.osty:18554:13
			func() struct{} { params = append(params, p); return struct{}{} }()
		}
	}
	return astbridge.ClosureExprNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), params, astLowerType(arena, toks, n.right), astLowerExpr(arena, toks, n.left))
}

// Osty: /tmp/selfhost_merged.osty:18560:1
func astLowerArg(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Arg {
	// Osty: /tmp/selfhost_merged.osty:18561:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18562:9
		return astbridge.NilArg()
	}
	// Osty: /tmp/selfhost_merged.osty:18564:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18565:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNField_{})) {
		// Osty: /tmp/selfhost_merged.osty:18566:9
		return astbridge.ArgNode(astLowerNodePos(toks, n), n.text, astLowerExpr(arena, toks, n.left))
	}
	return astbridge.ArgNode(astLowerNodePos(toks, n), "", astLowerExpr(arena, toks, idx))
}

// Osty: /tmp/selfhost_merged.osty:18571:1
func astLowerMatchArm(arena *AstArena, toks []astbridge.Token, idx int) astbridge.MatchArm {
	// Osty: /tmp/selfhost_merged.osty:18572:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18573:9
		return astbridge.NilMatchArm()
	}
	// Osty: /tmp/selfhost_merged.osty:18575:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18576:5
	guard := astbridge.NilExpr()
	_ = guard
	// Osty: /tmp/selfhost_merged.osty:18577:5
	if astLowerIntListCount(n.children) > 0 {
		// Osty: /tmp/selfhost_merged.osty:18578:9
		guard = astLowerExpr(arena, toks, astLowerIntListAt(n.children, 0))
	}
	return astbridge.MatchArmNode(astLowerNodePos(toks, n), astLowerPattern(arena, toks, n.left), guard, astLowerExpr(arena, toks, n.right))
}

// Osty: /tmp/selfhost_merged.osty:18583:1
func astLowerChildPattern(arena *AstArena, toks []astbridge.Token, n *AstNode, at int) astbridge.Pattern {
	// Osty: /tmp/selfhost_merged.osty:18584:5
	if at < 0 || at >= astLowerIntListCount(n.children) {
		// Osty: /tmp/selfhost_merged.osty:18585:9
		return astbridge.NilPattern()
	}
	return astLowerPattern(arena, toks, astLowerIntListAt(n.children, at))
}

// Osty: /tmp/selfhost_merged.osty:18590:1
func astLowerChildExpr(arena *AstArena, toks []astbridge.Token, n *AstNode, at int) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18591:5
	if at < 0 || at >= astLowerIntListCount(n.children) {
		// Osty: /tmp/selfhost_merged.osty:18592:9
		return astbridge.NilExpr()
	}
	return astLowerExpr(arena, toks, astLowerIntListAt(n.children, at))
}

// Osty: /tmp/selfhost_merged.osty:18597:1
func astLowerPattern(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Pattern {
	// Osty: /tmp/selfhost_merged.osty:18598:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18599:9
		return astbridge.NilPattern()
	}
	// Osty: /tmp/selfhost_merged.osty:18601:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18602:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNIdent{})) {
		// Osty: /tmp/selfhost_merged.osty:18603:9
		return astbridge.IdentPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.text)
	}
	// Osty: /tmp/selfhost_merged.osty:18605:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNTuple{})) {
		// Osty: /tmp/selfhost_merged.osty:18606:9
		return astLowerTuplePat(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18608:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNIntLit{})) || ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNFloatLit{})) || ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNStringLit{})) || ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNCharLit{})) || ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNByteLit{})) || ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNBoolLit{})) {
		// Osty: /tmp/selfhost_merged.osty:18609:9
		return astbridge.LiteralPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerExpr(arena, toks, idx))
	}
	// Osty: /tmp/selfhost_merged.osty:18611:5
	if ostyEqual(n.kind, AstNodeKind(&AstNodeKind_AstNPattern{})) && n.extra > 0 {
		// Osty: /tmp/selfhost_merged.osty:18612:9
		return astLowerStructuredPattern(arena, toks, n)
	}
	return astLowerTextPattern(arena, toks, n)
}

// Osty: /tmp/selfhost_merged.osty:18617:1
func astLowerTuplePat(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Pattern {
	// Osty: /tmp/selfhost_merged.osty:18618:5
	elems := astbridge.EmptyPatternList()
	_ = elems
	// Osty: /tmp/selfhost_merged.osty:18619:5
	for _, child := range n.children {
		// Osty: /tmp/selfhost_merged.osty:18620:9
		p := astLowerPattern(arena, toks, child)
		_ = p
		// Osty: /tmp/selfhost_merged.osty:18621:9
		if !(astbridge.IsNilPattern(p)) {
			// Osty: /tmp/selfhost_merged.osty:18622:13
			func() struct{} { elems = append(elems, p); return struct{}{} }()
		}
	}
	return astbridge.TuplePatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), elems)
}

// Osty: /tmp/selfhost_merged.osty:18628:1
func astLowerStructuredPattern(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Pattern {
	// Osty: /tmp/selfhost_merged.osty:18629:5
	if n.extra == astPatternIdentKind() {
		// Osty: /tmp/selfhost_merged.osty:18630:9
		return astbridge.IdentPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.text)
	}
	// Osty: /tmp/selfhost_merged.osty:18632:5
	if n.extra == astPatternWildcardKind() {
		// Osty: /tmp/selfhost_merged.osty:18633:9
		return astbridge.WildcardPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n))
	}
	// Osty: /tmp/selfhost_merged.osty:18635:5
	if n.extra == astPatternLiteralKind() {
		// Osty: /tmp/selfhost_merged.osty:18636:9
		return astbridge.LiteralPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerPatternLiteralExprNode(arena, toks, n))
	}
	// Osty: /tmp/selfhost_merged.osty:18638:5
	if n.extra == astPatternTupleKind() {
		// Osty: /tmp/selfhost_merged.osty:18639:9
		return astLowerTuplePat(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18641:5
	if n.extra == astPatternVariantKind() {
		// Osty: /tmp/selfhost_merged.osty:18642:9
		args := astbridge.EmptyPatternList()
		_ = args
		// Osty: /tmp/selfhost_merged.osty:18643:9
		for _, child := range n.children {
			// Osty: /tmp/selfhost_merged.osty:18644:13
			p := astLowerPattern(arena, toks, child)
			_ = p
			// Osty: /tmp/selfhost_merged.osty:18645:13
			if !(astbridge.IsNilPattern(p)) {
				// Osty: /tmp/selfhost_merged.osty:18646:17
				func() struct{} { args = append(args, p); return struct{}{} }()
			}
		}
		// Osty: /tmp/selfhost_merged.osty:18649:9
		return astbridge.VariantPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerSplitPath(n.text), args)
	}
	// Osty: /tmp/selfhost_merged.osty:18651:5
	if n.extra == astPatternStructKind() {
		// Osty: /tmp/selfhost_merged.osty:18652:9
		fields := astbridge.EmptyStructPatFieldList()
		_ = fields
		// Osty: /tmp/selfhost_merged.osty:18653:9
		for _, child := range n.children {
			// Osty: /tmp/selfhost_merged.osty:18654:13
			cn := astArenaNodeAt(arena, child)
			_ = cn
			// Osty: /tmp/selfhost_merged.osty:18655:13
			if cn.extra == astPatternFieldKind() {
				// Osty: /tmp/selfhost_merged.osty:18656:17
				func() struct{} {
					fields = append(fields, astbridge.StructPatFieldNode(astLowerNodePos(toks, cn), cn.text, astLowerPattern(arena, toks, cn.left)))
					return struct{}{}
				}()
			} else {
				// Osty: /tmp/selfhost_merged.osty:18658:17
				pat := astLowerPattern(arena, toks, child)
				_ = pat
				// Osty: /tmp/selfhost_merged.osty:18659:17
				if cn.extra == astPatternIdentKind() {
					// Osty: /tmp/selfhost_merged.osty:18660:21
					func() struct{} {
						fields = append(fields, astbridge.StructPatFieldNode(astLowerNodePos(toks, cn), cn.text, astbridge.NilPattern()))
						return struct{}{}
					}()
				} else if !(astbridge.IsNilPattern(pat)) {
					// Osty: /tmp/selfhost_merged.osty:18662:21
					func() struct{} {
						fields = append(fields, astbridge.StructPatFieldNode(astLowerNodePos(toks, cn), "", pat))
						return struct{}{}
					}()
				}
			}
		}
		// Osty: /tmp/selfhost_merged.osty:18666:9
		return astbridge.StructPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerSplitPath(n.text), fields, n.flags == 1)
	}
	// Osty: /tmp/selfhost_merged.osty:18668:5
	if n.extra == astPatternBindingKind() {
		// Osty: /tmp/selfhost_merged.osty:18669:9
		return astbridge.BindingPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), n.text, astLowerPattern(arena, toks, n.left))
	}
	// Osty: /tmp/selfhost_merged.osty:18671:5
	if n.extra == astPatternRangeKind() {
		// Osty: /tmp/selfhost_merged.osty:18672:9
		return astbridge.RangePatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerPatternLiteralExpr(arena, toks, n.left), astLowerPatternLiteralExpr(arena, toks, n.right), n.flags == 1)
	}
	// Osty: /tmp/selfhost_merged.osty:18674:5
	if n.extra == astPatternOrKind() {
		// Osty: /tmp/selfhost_merged.osty:18675:9
		return astbridge.OrPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerOrAlts(arena, toks, n))
	}
	return astbridge.WildcardPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n))
}

// Osty: /tmp/selfhost_merged.osty:18680:1
func astLowerTextPattern(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Pattern {
	// Osty: /tmp/selfhost_merged.osty:18681:5
	if strings.HasPrefix(n.text, "ident:") {
		// Osty: /tmp/selfhost_merged.osty:18682:9
		return astbridge.IdentPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), strings.TrimPrefix(n.text, "ident:"))
	}
	// Osty: /tmp/selfhost_merged.osty:18684:5
	if n.text == "wildcard" {
		// Osty: /tmp/selfhost_merged.osty:18685:9
		return astbridge.WildcardPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n))
	}
	// Osty: /tmp/selfhost_merged.osty:18687:5
	if strings.HasPrefix(n.text, "literal:") {
		// Osty: /tmp/selfhost_merged.osty:18688:9
		return astbridge.LiteralPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerLiteralPatternExpr(toks, n))
	}
	// Osty: /tmp/selfhost_merged.osty:18690:5
	if n.text == "negLiteral" {
		// Osty: /tmp/selfhost_merged.osty:18691:9
		return astbridge.LiteralPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astbridge.UnaryExprNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerKind(FrontTokenKind(&FrontTokenKind_FrontMinus{})), astLowerLiteralPatternExpr(toks, astArenaNodeAt(arena, n.left))))
	}
	// Osty: /tmp/selfhost_merged.osty:18697:5
	if n.text == "tuple" {
		// Osty: /tmp/selfhost_merged.osty:18698:9
		return astLowerTuplePat(arena, toks, n)
	}
	// Osty: /tmp/selfhost_merged.osty:18700:5
	if strings.HasPrefix(n.text, "variant:") {
		// Osty: /tmp/selfhost_merged.osty:18701:9
		args := astbridge.EmptyPatternList()
		_ = args
		// Osty: /tmp/selfhost_merged.osty:18702:9
		for _, child := range n.children {
			// Osty: /tmp/selfhost_merged.osty:18703:13
			p := astLowerPattern(arena, toks, child)
			_ = p
			// Osty: /tmp/selfhost_merged.osty:18704:13
			if !(astbridge.IsNilPattern(p)) {
				// Osty: /tmp/selfhost_merged.osty:18705:17
				func() struct{} { args = append(args, p); return struct{}{} }()
			}
		}
		// Osty: /tmp/selfhost_merged.osty:18708:9
		return astbridge.VariantPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerSplitPath(strings.TrimPrefix(n.text, "variant:")), args)
	}
	// Osty: /tmp/selfhost_merged.osty:18710:5
	if strings.HasPrefix(n.text, "struct:") {
		// Osty: /tmp/selfhost_merged.osty:18711:9
		fields := astbridge.EmptyStructPatFieldList()
		_ = fields
		// Osty: /tmp/selfhost_merged.osty:18712:9
		for _, child := range n.children {
			// Osty: /tmp/selfhost_merged.osty:18713:13
			cn := astArenaNodeAt(arena, child)
			_ = cn
			// Osty: /tmp/selfhost_merged.osty:18714:13
			if cn.text != "" && cn.left >= 0 {
				// Osty: /tmp/selfhost_merged.osty:18715:17
				func() struct{} {
					fields = append(fields, astbridge.StructPatFieldNode(astLowerNodePos(toks, cn), cn.text, astLowerPattern(arena, toks, cn.left)))
					return struct{}{}
				}()
			} else {
				// Osty: /tmp/selfhost_merged.osty:18717:17
				pat := astLowerPattern(arena, toks, child)
				_ = pat
				// Osty: /tmp/selfhost_merged.osty:18718:17
				childNode := astArenaNodeAt(arena, child)
				_ = childNode
				// Osty: /tmp/selfhost_merged.osty:18719:17
				if ostyEqual(childNode.kind, AstNodeKind(&AstNodeKind_AstNIdent{})) {
					// Osty: /tmp/selfhost_merged.osty:18720:21
					func() struct{} {
						fields = append(fields, astbridge.StructPatFieldNode(astLowerNodePos(toks, childNode), childNode.text, astbridge.NilPattern()))
						return struct{}{}
					}()
				} else if strings.HasPrefix(childNode.text, "ident:") {
					// Osty: /tmp/selfhost_merged.osty:18722:21
					func() struct{} {
						fields = append(fields, astbridge.StructPatFieldNode(astLowerNodePos(toks, childNode), strings.TrimPrefix(childNode.text, "ident:"), astbridge.NilPattern()))
						return struct{}{}
					}()
				} else if !(astbridge.IsNilPattern(pat)) {
					// Osty: /tmp/selfhost_merged.osty:18724:21
					func() struct{} {
						fields = append(fields, astbridge.StructPatFieldNode(astLowerNodePos(toks, childNode), "", pat))
						return struct{}{}
					}()
				}
			}
		}
		// Osty: /tmp/selfhost_merged.osty:18728:9
		return astbridge.StructPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerSplitPath(strings.TrimPrefix(n.text, "struct:")), fields, n.flags == 1)
	}
	// Osty: /tmp/selfhost_merged.osty:18730:5
	if strings.HasPrefix(n.text, "binding:") {
		// Osty: /tmp/selfhost_merged.osty:18731:9
		return astbridge.BindingPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), strings.TrimPrefix(n.text, "binding:"), astLowerPattern(arena, toks, n.left))
	}
	// Osty: /tmp/selfhost_merged.osty:18733:5
	if n.text == "range" {
		// Osty: /tmp/selfhost_merged.osty:18734:9
		return astbridge.RangePatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerPatternLiteralExpr(arena, toks, n.left), astLowerPatternLiteralExpr(arena, toks, n.right), n.flags == 1)
	}
	// Osty: /tmp/selfhost_merged.osty:18736:5
	if n.text == "or" {
		// Osty: /tmp/selfhost_merged.osty:18737:9
		return astbridge.OrPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerOrAlts(arena, toks, n))
	}
	return astbridge.WildcardPatNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n))
}

// Osty: /tmp/selfhost_merged.osty:18742:1
func astLowerOrAlts(arena *AstArena, toks []astbridge.Token, n *AstNode) []astbridge.Pattern {
	// Osty: /tmp/selfhost_merged.osty:18743:5
	out := astbridge.EmptyPatternList()
	_ = out
	// Osty: /tmp/selfhost_merged.osty:18744:5
	out = astLowerCollectOrAlt(arena, toks, n.left, out)
	return astLowerCollectOrAlt(arena, toks, n.right, out)
}

// Osty: /tmp/selfhost_merged.osty:18748:1
func astLowerCollectOrAlt(arena *AstArena, toks []astbridge.Token, idx int, out []astbridge.Pattern) []astbridge.Pattern {
	// Osty: /tmp/selfhost_merged.osty:18749:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18750:9
		return out
	}
	// Osty: /tmp/selfhost_merged.osty:18752:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	// Osty: /tmp/selfhost_merged.osty:18753:5
	if n.extra == astPatternOrKind() || n.text == "or" {
		// Osty: /tmp/selfhost_merged.osty:18754:9
		left := astLowerCollectOrAlt(arena, toks, n.left, out)
		_ = left
		// Osty: /tmp/selfhost_merged.osty:18755:9
		return astLowerCollectOrAlt(arena, toks, n.right, left)
	}
	// Osty: /tmp/selfhost_merged.osty:18757:5
	p := astLowerPattern(arena, toks, idx)
	_ = p
	// Osty: /tmp/selfhost_merged.osty:18758:5
	if !(astbridge.IsNilPattern(p)) {
		// Osty: /tmp/selfhost_merged.osty:18759:9
		func() struct{} { out = append(out, p); return struct{}{} }()
	}
	return out
}

// Osty: /tmp/selfhost_merged.osty:18764:1
func astLowerPatternLiteralExpr(arena *AstArena, toks []astbridge.Token, idx int) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18765:5
	if idx < 0 {
		// Osty: /tmp/selfhost_merged.osty:18766:9
		return astbridge.NilExpr()
	}
	// Osty: /tmp/selfhost_merged.osty:18768:5
	n := astArenaNodeAt(arena, idx)
	_ = n
	return astLowerPatternLiteralExprNode(arena, toks, n)
}

// Osty: /tmp/selfhost_merged.osty:18772:1
func astLowerPatternLiteralExprNode(arena *AstArena, toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18773:5
	if n.text == "negLiteral" || (n.extra == astPatternLiteralKind() && n.text == "-" && n.left >= 0) {
		// Osty: /tmp/selfhost_merged.osty:18774:9
		return astbridge.UnaryExprNode(astLowerNodePos(toks, n), astLowerNodeEnd(toks, n), astLowerKind(FrontTokenKind(&FrontTokenKind_FrontMinus{})), astLowerLiteralPatternExpr(toks, astArenaNodeAt(arena, n.left)))
	}
	return astLowerLiteralPatternExpr(toks, n)
}

// Osty: /tmp/selfhost_merged.osty:18779:1
func astLowerLiteralPatternExpr(toks []astbridge.Token, n *AstNode) astbridge.Expr {
	// Osty: /tmp/selfhost_merged.osty:18780:5
	pos := astLowerNodePos(toks, n)
	_ = pos
	// Osty: /tmp/selfhost_merged.osty:18781:5
	end := astLowerNodeEnd(toks, n)
	_ = end
	// Osty: /tmp/selfhost_merged.osty:18782:5
	text := strings.TrimPrefix(n.text, "literal:")
	_ = text
	// Osty: /tmp/selfhost_merged.osty:18783:5
	if ostyEqual(n.op, FrontTokenKind(&FrontTokenKind_FrontInt{})) {
		// Osty: /tmp/selfhost_merged.osty:18784:9
		return astbridge.IntLitExpr(pos, end, text)
	}
	// Osty: /tmp/selfhost_merged.osty:18786:5
	if ostyEqual(n.op, FrontTokenKind(&FrontTokenKind_FrontFloat{})) {
		// Osty: /tmp/selfhost_merged.osty:18787:9
		return astbridge.FloatLitExpr(pos, end, text)
	}
	// Osty: /tmp/selfhost_merged.osty:18789:5
	if ostyEqual(n.op, FrontTokenKind(&FrontTokenKind_FrontString{})) || ostyEqual(n.op, FrontTokenKind(&FrontTokenKind_FrontRawString{})) {
		// Osty: /tmp/selfhost_merged.osty:18790:9
		return astbridge.StringLitExpr(pos, end, astLowerStringContent(text))
	}
	// Osty: /tmp/selfhost_merged.osty:18792:5
	if ostyEqual(n.op, FrontTokenKind(&FrontTokenKind_FrontChar{})) {
		// Osty: /tmp/selfhost_merged.osty:18793:9
		return astbridge.CharLitExpr(pos, end, astLowerDecodedLiteral(text))
	}
	// Osty: /tmp/selfhost_merged.osty:18795:5
	if ostyEqual(n.op, FrontTokenKind(&FrontTokenKind_FrontByte{})) {
		// Osty: /tmp/selfhost_merged.osty:18796:9
		return astbridge.ByteLitExpr(pos, end, astLowerDecodedLiteral(text))
	}
	return astbridge.BoolLitExpr(pos, end, text == "true")
}

// Osty: /tmp/selfhost_merged.osty:18801:1
func astLowerIntListCount(xs []int) int {
	// Osty: /tmp/selfhost_merged.osty:18802:5
	count := 0
	_ = count
	// Osty: /tmp/selfhost_merged.osty:18803:5
	for _, x := range xs {
		// Osty: /tmp/selfhost_merged.osty:18804:9
		_ = x
		// Osty: /tmp/selfhost_merged.osty:18805:9
		func() {
			var _cur2544 int = count
			var _rhs2545 int = 1
			if _rhs2545 > 0 && _cur2544 > math.MaxInt-_rhs2545 {
				panic("integer overflow")
			}
			if _rhs2545 < 0 && _cur2544 < math.MinInt-_rhs2545 {
				panic("integer overflow")
			}
			count = _cur2544 + _rhs2545
		}()
	}
	return count
}

// Osty: /tmp/selfhost_merged.osty:18810:1
func astLowerIntListAt(xs []int, target int) int {
	// Osty: /tmp/selfhost_merged.osty:18811:5
	i := 0
	_ = i
	// Osty: /tmp/selfhost_merged.osty:18812:5
	for _, x := range xs {
		// Osty: /tmp/selfhost_merged.osty:18813:9
		if i == target {
			// Osty: /tmp/selfhost_merged.osty:18814:13
			return x
		}
		// Osty: /tmp/selfhost_merged.osty:18816:9
		func() {
			var _cur2546 int = i
			var _rhs2547 int = 1
			if _rhs2547 > 0 && _cur2546 > math.MaxInt-_rhs2547 {
				panic("integer overflow")
			}
			if _rhs2547 < 0 && _cur2546 < math.MinInt-_rhs2547 {
				panic("integer overflow")
			}
			i = _cur2546 + _rhs2547
		}()
	}
	return -1
}
