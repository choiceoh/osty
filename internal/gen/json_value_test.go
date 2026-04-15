package gen_test

import (
	"strings"
	"testing"
)

func TestStdJSONValueAPI(t *testing.T) {
	src := "use std.json\n\n" +
		"fn main() {\n" +
		"    let obj: json.Json = json.Object({\"name\": json.String(\"osty\"), \"version\": json.Number(1.0), \"active\": json.Bool(true)})\n" +
		"    let text = json.stringifyValue(obj)\n" +
		"    println(text)\n" +
		"\n" +
		"    let parsed = json.parseValue(text).unwrap()\n" +
		"    println(json.isObject(parsed))\n" +
		"    println(json.isNull(parsed))\n" +
		"\n" +
		"    let name = json.getField(parsed, \"name\").unwrap()\n" +
		"    println(json.isString(name))\n" +
		"    println(json.asString(name).unwrap())\n" +
		"\n" +
		"    let ver = json.getField(parsed, \"version\").unwrap()\n" +
		"    println(json.isNumber(ver))\n" +
		"    println(json.asNumber(ver).unwrap())\n" +
		"\n" +
		"    let active = json.getField(parsed, \"active\").unwrap()\n" +
		"    println(json.isBool(active))\n" +
		"    println(json.asBool(active).unwrap())\n" +
		"\n" +
		"    println(json.getField(parsed, \"missing\").isErr())\n" +
		"\n" +
		"    let arr: json.Json = json.Array([json.Number(10.0), json.Number(20.0)])\n" +
		"    let arrText = json.stringifyValue(arr)\n" +
		"    println(arrText)\n" +
		"    let parsedArr = json.parseValue(arrText).unwrap()\n" +
		"    println(json.isArray(parsedArr))\n" +
		"    let first = json.getIndex(parsedArr, 0).unwrap()\n" +
		"    println(json.asNumber(first).unwrap())\n" +
		"    println(json.getIndex(parsedArr, 5).isErr())\n" +
		"\n" +
		"    println(json.isNull(json.Null))\n" +
		"\n" +
		"    println(json.parseValue(\"invalid json\\{\").isErr())\n" +
		"}\n"

	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		`{"active":true,"name":"osty","version":1}`,
		`true`,
		`false`,
		`true`,
		`osty`,
		`true`,
		`1`,
		`true`,
		`true`,
		`true`,
		`[10,20]`,
		`true`,
		`10`,
		`true`,
		`true`,
		`true`,
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{
		"jsonStringifyValue",
		"jsonParseValueResult",
		"jsonIsNullVal",
		"jsonAsBoolResult",
		"jsonGetFieldResult",
		"jsonGetIndexResult",
	} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated JSON value API missing %s:\n%s", want, goSrc)
		}
	}
}
