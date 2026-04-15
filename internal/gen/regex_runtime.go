package gen

// regexRuntime is the pure-Go backtracking NFA regex engine emitted when
// std.regex is used. It does NOT depend on Go's regexp package.
const regexRuntime = `
// ── regex runtime: pure backtracking NFA engine ──────────────────

type rxRange struct{ lo, hi rune }

// inst is one NFA bytecode instruction.
// ops: 0=char 1=any 2=class 3=split 4=jump 5=save 6=match
//      7=anchorStart 8=anchorEnd 9=wordBound 10=nonWordBound
type rxInst struct {
	op      int
	c       rune
	ranges  []rxRange
	negated bool
	x, y    int
}

// rnode is a regex AST node.
// kinds: 0=literal 1=dot 2=anchorS 3=anchorE 4=class 5=concat
//        6=alt 7=group 8=repeat 9=wb 10=nwb 11=empty
type rnode struct {
	kind     int
	c        rune
	ranges   []rxRange
	negated  bool
	children []*rnode
	groupID  int
	name     string
	rmin     int
	rmax     int
	greedy   bool
}

// ── parser ───────────────────────────────────────────────────────

type rxParser struct {
	src    []rune
	pos    int
	groups int
	names  map[string]int
}

func rxParse(pattern string) (*rnode, int, map[string]int, error) {
	p := &rxParser{src: []rune(pattern), groups: 1, names: map[string]int{}}
	n, err := p.parseAlt()
	if err != nil {
		return nil, 0, nil, err
	}
	if p.pos < len(p.src) {
		return nil, 0, nil, fmt.Errorf("unexpected char in pattern")
	}
	return n, p.groups, p.names, nil
}

func (p *rxParser) at(c rune) bool  { return p.pos < len(p.src) && p.src[p.pos] == c }
func (p *rxParser) done() bool      { return p.pos >= len(p.src) }
func (p *rxParser) adv() rune       { c := p.src[p.pos]; p.pos++; return c }
func (p *rxParser) digitHere() bool { return p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' }

func (p *rxParser) parseAlt() (*rnode, error) {
	left, err := p.parseSeq()
	if err != nil {
		return nil, err
	}
	if p.at('|') {
		p.adv()
		right, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		return &rnode{kind: 6, children: []*rnode{left, right}}, nil
	}
	return left, nil
}

func (p *rxParser) parseSeq() (*rnode, error) {
	var items []*rnode
	for !p.done() && !p.at('|') && !p.at(')') {
		n, err := p.parseQuant()
		if err != nil {
			return nil, err
		}
		items = append(items, n)
	}
	if len(items) == 0 {
		return &rnode{kind: 11}, nil
	}
	if len(items) == 1 {
		return items[0], nil
	}
	return &rnode{kind: 5, children: items}, nil
}

func (p *rxParser) parseQuant() (*rnode, error) {
	atom, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	if p.done() {
		return atom, nil
	}
	c := p.src[p.pos]
	rmin, rmax, hasQ := 0, -1, false
	switch c {
	case '*':
		p.adv(); rmin = 0; rmax = -1; hasQ = true
	case '+':
		p.adv(); rmin = 1; rmax = -1; hasQ = true
	case '?':
		p.adv(); rmin = 0; rmax = 1; hasQ = true
	case '{':
		saved := p.pos
		if mn, mx, ok := p.tryBrace(); ok {
			rmin = mn; rmax = mx; hasQ = true
		} else {
			p.pos = saved
		}
	}
	if !hasQ {
		return atom, nil
	}
	greedy := true
	if p.at('?') {
		p.adv(); greedy = false
	}
	return &rnode{kind: 8, children: []*rnode{atom}, rmin: rmin, rmax: rmax, greedy: greedy}, nil
}

func (p *rxParser) tryBrace() (int, int, bool) {
	p.adv() // skip {
	if p.done() || !p.digitHere() {
		return 0, 0, false
	}
	n := p.readInt()
	if p.at('}') {
		p.adv(); return n, n, true
	}
	if p.at(',') {
		p.adv()
		if p.at('}') {
			p.adv(); return n, -1, true
		}
		if !p.done() && p.digitHere() {
			m := p.readInt()
			if p.at('}') && m >= n {
				p.adv(); return n, m, true
			}
		}
	}
	return 0, 0, false
}

func (p *rxParser) readInt() int {
	n := 0
	for !p.done() && p.digitHere() {
		n = n*10 + int(p.src[p.pos]-'0'); p.adv()
	}
	return n
}

func (p *rxParser) parseAtom() (*rnode, error) {
	if p.done() {
		return nil, fmt.Errorf("unexpected end of pattern")
	}
	c := p.src[p.pos]
	switch {
	case c == '.':
		p.adv(); return &rnode{kind: 1}, nil
	case c == '^':
		p.adv(); return &rnode{kind: 2}, nil
	case c == '$':
		p.adv(); return &rnode{kind: 3}, nil
	case c == '(':
		return p.parseGroup()
	case c == '[':
		return p.parseClass()
	case c == '\\':
		return p.parseEsc()
	case c == '*' || c == '+' || c == '?':
		return nil, fmt.Errorf("quantifier without preceding element")
	default:
		p.adv(); return &rnode{kind: 0, c: c}, nil
	}
}

func (p *rxParser) parseGroup() (*rnode, error) {
	p.adv() // skip (
	if p.at('?') {
		p.adv()
		if p.at(':') {
			p.adv()
			inner, err := p.parseAlt()
			if err != nil {
				return nil, err
			}
			if !p.at(')') {
				return nil, fmt.Errorf("expected ')'")
			}
			p.adv()
			return &rnode{kind: 7, children: []*rnode{inner}, groupID: -1}, nil
		}
		if p.at('P') || p.at('<') {
			if p.at('P') {
				p.adv()
			}
			if !p.at('<') {
				return nil, fmt.Errorf("expected '<' in named group")
			}
			p.adv()
			var name []rune
			for !p.done() && !p.at('>') {
				name = append(name, p.adv())
			}
			if !p.at('>') {
				return nil, fmt.Errorf("expected '>'")
			}
			p.adv()
			id := p.groups; p.groups++
			nm := string(name)
			p.names[nm] = id
			inner, err := p.parseAlt()
			if err != nil {
				return nil, err
			}
			if !p.at(')') {
				return nil, fmt.Errorf("expected ')'")
			}
			p.adv()
			return &rnode{kind: 7, children: []*rnode{inner}, groupID: id, name: nm}, nil
		}
		return nil, fmt.Errorf("unknown group modifier")
	}
	id := p.groups; p.groups++
	inner, err := p.parseAlt()
	if err != nil {
		return nil, err
	}
	if !p.at(')') {
		return nil, fmt.Errorf("expected ')'")
	}
	p.adv()
	return &rnode{kind: 7, children: []*rnode{inner}, groupID: id}, nil
}

func rxDigitRanges() []rxRange { return []rxRange{{'0', '9'}} }
func rxWordRanges() []rxRange {
	return []rxRange{{'a', 'z'}, {'A', 'Z'}, {'0', '9'}, {'_', '_'}}
}
func rxSpaceRanges() []rxRange {
	return []rxRange{{' ', ' '}, {'\t', '\t'}, {'\n', '\n'}, {'\r', '\r'}}
}

func (p *rxParser) parseClass() (*rnode, error) {
	p.adv() // skip [
	neg := false
	if p.at('^') {
		neg = true; p.adv()
	}
	var ranges []rxRange
	first := true
	for !p.done() && (first || !p.at(']')) {
		first = false
		if p.at('\\') {
			p.adv()
			if p.done() {
				return nil, fmt.Errorf("incomplete escape in class")
			}
			ec := p.adv()
			if ex := rxExpandEsc(ec); len(ex) > 0 {
				ranges = append(ranges, ex...)
			} else {
				lo := rxEscChar(ec)
				if p.canRange() {
					p.adv()
					hi, err := p.classChar()
					if err != nil {
						return nil, err
					}
					ranges = append(ranges, rxRange{lo, hi})
				} else {
					ranges = append(ranges, rxRange{lo, lo})
				}
			}
		} else {
			lo := p.adv()
			if p.canRange() {
				p.adv()
				hi, err := p.classChar()
				if err != nil {
					return nil, err
				}
				ranges = append(ranges, rxRange{lo, hi})
			} else {
				ranges = append(ranges, rxRange{lo, lo})
			}
		}
	}
	if !p.at(']') {
		return nil, fmt.Errorf("unterminated character class")
	}
	p.adv()
	return &rnode{kind: 4, ranges: ranges, negated: neg}, nil
}

func (p *rxParser) canRange() bool {
	return p.at('-') && p.pos+1 < len(p.src) && p.src[p.pos+1] != ']'
}

func (p *rxParser) classChar() (rune, error) {
	if p.done() {
		return 0, fmt.Errorf("incomplete class range")
	}
	if p.at('\\') {
		p.adv()
		if p.done() {
			return 0, fmt.Errorf("incomplete escape")
		}
		return rxEscChar(p.adv()), nil
	}
	return p.adv(), nil
}

func (p *rxParser) parseEsc() (*rnode, error) {
	p.adv() // skip backslash
	if p.done() {
		return nil, fmt.Errorf("trailing backslash")
	}
	c := p.adv()
	switch c {
	case 'd':
		return &rnode{kind: 4, ranges: rxDigitRanges()}, nil
	case 'D':
		return &rnode{kind: 4, ranges: rxDigitRanges(), negated: true}, nil
	case 'w':
		return &rnode{kind: 4, ranges: rxWordRanges()}, nil
	case 'W':
		return &rnode{kind: 4, ranges: rxWordRanges(), negated: true}, nil
	case 's':
		return &rnode{kind: 4, ranges: rxSpaceRanges()}, nil
	case 'S':
		return &rnode{kind: 4, ranges: rxSpaceRanges(), negated: true}, nil
	case 'b':
		return &rnode{kind: 9}, nil
	case 'B':
		return &rnode{kind: 10}, nil
	default:
		return &rnode{kind: 0, c: rxEscChar(c)}, nil
	}
}

func rxEscChar(c rune) rune {
	switch c {
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	}
	return c
}

func rxExpandEsc(c rune) []rxRange {
	switch c {
	case 'd':
		return rxDigitRanges()
	case 'w':
		return rxWordRanges()
	case 's':
		return rxSpaceRanges()
	}
	return nil
}

// ── compiler (AST → bytecode) ────────────────────────────────────

type rxCompiler struct{ code []rxInst }

func (cc *rxCompiler) emit(inst rxInst) int {
	idx := len(cc.code); cc.code = append(cc.code, inst); return idx
}

func (cc *rxCompiler) gen(n *rnode) {
	switch n.kind {
	case 0:
		cc.emit(rxInst{op: 0, c: n.c})
	case 1:
		cc.emit(rxInst{op: 1})
	case 2:
		cc.emit(rxInst{op: 7})
	case 3:
		cc.emit(rxInst{op: 8})
	case 4:
		cc.emit(rxInst{op: 2, ranges: n.ranges, negated: n.negated})
	case 5:
		for _, ch := range n.children {
			cc.gen(ch)
		}
	case 6:
		cc.genAlt(n)
	case 7:
		cc.genGroup(n)
	case 8:
		cc.genRepeat(n)
	case 9:
		cc.emit(rxInst{op: 9})
	case 10:
		cc.emit(rxInst{op: 10})
	case 11:
		// empty — no code
	}
}

func (cc *rxCompiler) genAlt(n *rnode) {
	spc := cc.emit(rxInst{op: 3})
	cc.gen(n.children[0])
	jpc := cc.emit(rxInst{op: 4})
	rStart := len(cc.code)
	cc.gen(n.children[1])
	end := len(cc.code)
	cc.code[spc] = rxInst{op: 3, x: spc + 1, y: rStart}
	cc.code[jpc] = rxInst{op: 4, x: end}
}

func (cc *rxCompiler) genGroup(n *rnode) {
	if n.groupID >= 0 {
		cc.emit(rxInst{op: 5, x: n.groupID * 2})
	}
	cc.gen(n.children[0])
	if n.groupID >= 0 {
		cc.emit(rxInst{op: 5, x: n.groupID*2 + 1})
	}
}

func (cc *rxCompiler) genRepeat(n *rnode) {
	inner := n.children[0]
	for i := 0; i < n.rmin; i++ {
		cc.gen(inner)
	}
	if n.rmax == -1 {
		spc := cc.emit(rxInst{op: 3})
		body := len(cc.code)
		cc.gen(inner)
		cc.emit(rxInst{op: 4, x: spc})
		end := len(cc.code)
		if n.greedy {
			cc.code[spc] = rxInst{op: 3, x: body, y: end}
		} else {
			cc.code[spc] = rxInst{op: 3, x: end, y: body}
		}
	} else if n.rmax > n.rmin {
		opt := n.rmax - n.rmin
		splits := make([]int, 0, opt)
		for j := 0; j < opt; j++ {
			splits = append(splits, cc.emit(rxInst{op: 3}))
			cc.gen(inner)
		}
		end := len(cc.code)
		for _, spc := range splits {
			if n.greedy {
				cc.code[spc] = rxInst{op: 3, x: spc + 1, y: end}
			} else {
				cc.code[spc] = rxInst{op: 3, x: end, y: spc + 1}
			}
		}
	}
}

// ── VM ───────────────────────────────────────────────────────────

type rxVM struct {
	code  []rxInst
	chars []rune
	boff  []int // byte offset of each char; len = len(chars)+1
	slots []int
}

func (vm *rxVM) exec(startPC, startSP int) bool {
	pc, sp := startPC, startSP
	for {
		inst := vm.code[pc]
		switch inst.op {
		case 0: // char
			if sp >= len(vm.chars) || vm.chars[sp] != inst.c {
				return false
			}
			sp++; pc++
		case 1: // any
			if sp >= len(vm.chars) || vm.chars[sp] == '\n' {
				return false
			}
			sp++; pc++
		case 2: // class
			if sp >= len(vm.chars) {
				return false
			}
			if !rxCharIn(vm.chars[sp], inst.ranges, inst.negated) {
				return false
			}
			sp++; pc++
		case 3: // split
			if vm.exec(inst.x, sp) {
				return true
			}
			pc = inst.y
		case 4: // jump
			pc = inst.x
		case 5: // save
			old := vm.slots[inst.x]
			vm.slots[inst.x] = vm.boff[sp]
			if vm.exec(pc+1, sp) {
				return true
			}
			vm.slots[inst.x] = old
			return false
		case 6: // match
			return true
		case 7: // anchor start
			if sp != 0 {
				return false
			}
			pc++
		case 8: // anchor end
			if sp != len(vm.chars) {
				return false
			}
			pc++
		case 9: // word boundary
			if !vm.wordBound(sp) {
				return false
			}
			pc++
		case 10: // non-word boundary
			if vm.wordBound(sp) {
				return false
			}
			pc++
		default:
			return false
		}
	}
}

func (vm *rxVM) wordBound(sp int) bool {
	left := sp > 0 && rxIsWord(vm.chars[sp-1])
	right := sp < len(vm.chars) && rxIsWord(vm.chars[sp])
	return left != right
}

func rxIsWord(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func rxCharIn(c rune, ranges []rxRange, neg bool) bool {
	for _, r := range ranges {
		if c >= r.lo && c <= r.hi {
			return !neg
		}
	}
	return neg
}

// ── public API ───────────────────────────────────────────────────

type Regex struct {
	code      []rxInst
	numGroups int
	names     map[string]int
}

type RegexMatch struct {
	text       string
	start, end int
}

type Captures struct {
	slots []int
	text  string
	names map[string]int
}

func regexCompile(pattern string) Result[Regex, error] {
	ast, groups, names, err := rxParse(pattern)
	if err != nil {
		return resultErr[Regex, error](err)
	}
	cc := &rxCompiler{}
	cc.emit(rxInst{op: 5, x: 0})
	cc.gen(ast)
	cc.emit(rxInst{op: 5, x: 1})
	cc.emit(rxInst{op: 6})
	return resultOk[Regex, error](Regex{code: cc.code, numGroups: groups, names: names})
}

func rxByteOffsets(chars []rune) []int {
	off := make([]int, len(chars)+1)
	b := 0
	for i, c := range chars {
		off[i] = b
		b += utf8.RuneLen(c)
	}
	off[len(chars)] = b
	return off
}

func rxInitSlots(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = -1
	}
	return s
}

func (re Regex) runAt(chars []rune, boff []int, sp int) ([]int, bool) {
	nslots := re.numGroups * 2
	vm := &rxVM{code: re.code, chars: chars, boff: boff, slots: rxInitSlots(nslots)}
	if vm.exec(0, sp) {
		return vm.slots, true
	}
	return nil, false
}

func (re Regex) findSlots(text string) ([]int, bool) {
	chars := []rune(text)
	boff := rxByteOffsets(chars)
	return re.findSlotsFrom(chars, boff, 0)
}

func (re Regex) findSlotsFrom(chars []rune, boff []int, from int) ([]int, bool) {
	for sp := from; sp <= len(chars); sp++ {
		if sl, ok := re.runAt(chars, boff, sp); ok {
			return sl, true
		}
	}
	return nil, false
}

func (re Regex) matches(text string) bool {
	_, ok := re.findSlots(text)
	return ok
}

func (re Regex) find(text string) *RegexMatch {
	sl, ok := re.findSlots(text)
	if !ok {
		return nil
	}
	m := RegexMatch{text: text[sl[0]:sl[1]], start: sl[0], end: sl[1]}
	return &m
}

func rxByteToChar(boff []int, b int) int {
	for i, o := range boff {
		if o >= b {
			return i
		}
	}
	return len(boff) - 1
}

func (re Regex) findAll(text string) []RegexMatch {
	chars := []rune(text)
	boff := rxByteOffsets(chars)
	var out []RegexMatch
	for from := 0; from <= len(chars); {
		sl, ok := re.findSlotsFrom(chars, boff, from)
		if !ok {
			from++
			continue
		}
		out = append(out, RegexMatch{text: text[sl[0]:sl[1]], start: sl[0], end: sl[1]})
		ec := rxByteToChar(boff, sl[1])
		if ec > from {
			from = ec
		} else {
			from++
		}
	}
	return out
}

func (re Regex) captures(text string) *Captures {
	sl, ok := re.findSlots(text)
	if !ok {
		return nil
	}
	c := Captures{slots: sl, text: text, names: re.names}
	return &c
}

func (re Regex) capturesAll(text string) []Captures {
	chars := []rune(text)
	boff := rxByteOffsets(chars)
	var out []Captures
	for from := 0; from <= len(chars); {
		sl, ok := re.findSlotsFrom(chars, boff, from)
		if !ok {
			from++
			continue
		}
		out = append(out, Captures{slots: sl, text: text, names: re.names})
		ec := rxByteToChar(boff, sl[1])
		if ec > from {
			from = ec
		} else {
			from++
		}
	}
	return out
}

func rxSlotText(slots []int, gid int, text string) string {
	si := gid * 2
	if si+1 >= len(slots) {
		return ""
	}
	s, e := slots[si], slots[si+1]
	if s < 0 || e < 0 {
		return ""
	}
	return text[s:e]
}

func rxApplyRepl(tmpl string, slots []int, text string, names map[string]int) string {
	tc := []rune(tmpl)
	var out []byte
	for i := 0; i < len(tc); {
		if tc[i] == '$' {
			if i+1 < len(tc) && tc[i+1] == '$' {
				out = append(out, '$')
				i += 2
			} else if i+1 < len(tc) && tc[i+1] >= '0' && tc[i+1] <= '9' {
				j := i + 1
				n := 0
				for j < len(tc) && tc[j] >= '0' && tc[j] <= '9' {
					n = n*10 + int(tc[j]-'0'); j++
				}
				out = append(out, rxSlotText(slots, n, text)...)
				i = j
			} else if i+1 < len(tc) && tc[i+1] == '{' {
				j := i + 2
				var nm []rune
				for j < len(tc) && tc[j] != '}' {
					nm = append(nm, tc[j]); j++
				}
				if j < len(tc) {
					j++
				}
				key := string(nm)
				n := 0
				isNum := true
				for _, d := range nm {
					if d < '0' || d > '9' {
						isNum = false; break
					}
					n = n*10 + int(d-'0')
				}
				if isNum {
					out = append(out, rxSlotText(slots, n, text)...)
				} else if gid, ok := names[key]; ok {
					out = append(out, rxSlotText(slots, gid, text)...)
				}
				i = j
			} else {
				out = append(out, '$')
				i++
			}
		} else {
			out = append(out, string(tc[i])...)
			i++
		}
	}
	return string(out)
}

func (re Regex) replace(text, replacement string) string {
	sl, ok := re.findSlots(text)
	if !ok {
		return text
	}
	return text[:sl[0]] + rxApplyRepl(replacement, sl, text, re.names) + text[sl[1]:]
}

func (re Regex) replaceAll(text, replacement string) string {
	chars := []rune(text)
	boff := rxByteOffsets(chars)
	var out []byte
	lastEnd := 0
	for from := 0; from <= len(chars); {
		sl, ok := re.findSlotsFrom(chars, boff, from)
		if !ok {
			from++
			continue
		}
		out = append(out, text[lastEnd:sl[0]]...)
		out = append(out, rxApplyRepl(replacement, sl, text, re.names)...)
		lastEnd = sl[1]
		ec := rxByteToChar(boff, sl[1])
		if ec > from {
			from = ec
		} else {
			from++
		}
	}
	out = append(out, text[lastEnd:]...)
	return string(out)
}

func (re Regex) split(text string) []string {
	all := re.findAll(text)
	if len(all) == 0 {
		return []string{text}
	}
	var parts []string
	start := 0
	for _, m := range all {
		parts = append(parts, text[start:m.start])
		start = m.end
	}
	parts = append(parts, text[start:])
	return parts
}

func (c Captures) get(i int) *string {
	si := i * 2
	if si+1 >= len(c.slots) {
		return nil
	}
	s, e := c.slots[si], c.slots[si+1]
	if s < 0 || e < 0 {
		return nil
	}
	v := c.text[s:e]
	return &v
}

func (c Captures) named(name string) *string {
	gid, ok := c.names[name]
	if !ok {
		return nil
	}
	return c.get(gid)
}
`
