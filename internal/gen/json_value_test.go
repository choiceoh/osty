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

func TestStdJSONValueAPIDeepEdges(t *testing.T) {
	src := `use std.json
use std.bytes

fn main() {
    let text = "[[[\{\"leaf\":\"\\uD834\\uDD1E\",\"escaped\":\"line\\nquote\",\"n\":-12.5e2,\"arr\":[true,false,null]\}]]]"
    let parsed = json.parseValue(text).unwrap()
    let level1 = json.getIndex(parsed, 0).unwrap()
    let level2 = json.getIndex(level1, 0).unwrap()
    let obj = json.getIndex(level2, 0).unwrap()

    let leaf = json.getField(obj, "leaf").unwrap()
    let leafText = json.asString(leaf).unwrap()
    println(bytes.len(bytes.fromString(leafText)))
    println(bytes.len(bytes.fromString(json.stringifyValue(leaf))))

    let escaped = json.asString(json.getField(obj, "escaped").unwrap()).unwrap()
    println(bytes.contains(bytes.fromString(escaped), bytes.fromString("line")))
    println(escaped == "line\nquote")
    println(json.stringifyValue(json.String(escaped)))

    let num = json.asNumber(json.getField(obj, "n").unwrap()).unwrap()
    println(num)
    let arr = json.asArray(json.getField(obj, "arr").unwrap()).unwrap()
    println(arr.len())
    println(json.asBool(arr[0]).unwrap())
    println(json.isNull(arr[2]))

    println(json.asObject(obj).unwrap().len())
    println(json.asBool(obj).isErr())
    println(json.getIndex(parsed, -1).isErr())
    println(json.getField(parsed, "missing").isErr())
    println(json.parseValue("\"\\uD800\"").isErr())
    println(json.parseValue("\"\\uDD1E\"").isErr())
    println(json.parseValue("01").isErr())
    println(json.parseValue("[1,]").isErr())

    let built: json.Json = json.Array([json.Array([json.Object({"a": json.Null, "z": json.Number(2.0)})])])
    println(json.stringifyValue(built))
}
`

	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		`4`,
		`6`,
		`true`,
		`true`,
		`"line\nquote"`,
		`-1250`,
		`3`,
		`true`,
		`true`,
		`4`,
		`true`,
		`true`,
		`true`,
		`true`,
		`true`,
		`true`,
		`true`,
		`[[{"a":null,"z":2}]]`,
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
}
