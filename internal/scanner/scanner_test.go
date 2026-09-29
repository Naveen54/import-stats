package scanner

import (
	"reflect"
	"testing"
)

func specs(recs []Record) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, string(r.Kind)+":"+r.Specifier)
	}
	return out
}

func exportSpecs(exports []Export) []string {
	out := make([]string, 0, len(exports))
	for _, e := range exports {
		out = append(out, e.Kind+":"+e.Name)
	}
	return out
}

func exportLines(exports []Export) []int {
	out := make([]int, 0, len(exports))
	for _, e := range exports {
		out = append(out, e.Line)
	}
	return out
}

func TestBasicImports(t *testing.T) {
	src := `import 'babel-polyfill';
import React from 'react';
import { render } from 'react-dom';
import * as qs from 'common/client/qsGateway';
export * from './app';
export { a } from "./b";
const moment = require('moment');
const { apiCalls } = require('./utils/apiCalls');
const Page = lazy(() => import('pages/NoAccessPage'));
import './styles/index.scss';
`
	got := specs(Scan(src))
	want := []string{
		"side-effect:babel-polyfill",
		"import:react",
		"import:react-dom",
		"import:common/client/qsGateway",
		"export-from:./app",
		"export-from:./b",
		"require:moment",
		"require:./utils/apiCalls",
		"dynamic-import:pages/NoAccessPage",
		"side-effect:./styles/index.scss",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestIgnoresCommentsAndStrings(t *testing.T) {
	src := "// import 'ghost-a';\n" +
		"/* import 'ghost-b';\n   require('ghost-c'); */\n" +
		"const s = \"import 'ghost-d'\";\n" +
		"const t = `require('ghost-e')`;\n" +
		"import real from 'real-pkg';\n"
	got := specs(Scan(src))
	want := []string{"import:real-pkg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMultilineAndSymbols(t *testing.T) {
	src := `import DefaultThing, {
  alpha,
  beta as gamma,
  // delta,
} from '@scope/pkg';`
	recs := Scan(src)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d (%v)", len(recs), specs(recs))
	}
	want := []string{"default:DefaultThing", "alpha", "beta as gamma"}
	if !reflect.DeepEqual(recs[0].Symbols, want) {
		t.Fatalf("symbols got %v want %v", recs[0].Symbols, want)
	}
	if recs[0].Specifier != "@scope/pkg" {
		t.Fatalf("specifier %q", recs[0].Specifier)
	}
}

func TestNamespaceAndRequireSymbols(t *testing.T) {
	recs := Scan("import * as path from 'path';\nconst { a, b } = require('mod');\nconst c = require('mod2');")
	if recs[0].Symbols[0] != "*:path" {
		t.Fatalf("ns symbol %v", recs[0].Symbols)
	}
	if !reflect.DeepEqual(recs[1].Symbols, []string{"a", "b"}) {
		t.Fatalf("require symbols %v", recs[1].Symbols)
	}
	if !reflect.DeepEqual(recs[2].Symbols, []string{"default:c"}) {
		t.Fatalf("require default %v", recs[2].Symbols)
	}
}

func TestJSXAndRegexAndTemplates(t *testing.T) {
	src := "import React from 'react';\n" +
		"const re = /import 'nope'/g;\n" +
		"const div = <div a={b / c} title=\"require('nope2')\" />;\n" +
		"const url = `${base}/import/x`;\n" +
		"import after from 'after-pkg';\n"
	got := specs(Scan(src))
	want := []string{"import:react", "import:after-pkg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTypeScriptConstructs(t *testing.T) {
	src := `import type { Foo } from './types';
export type { Bar } from './bar';
function id<T>(x: T): T { return x; }
const a = 1 < 2 > 0;
import realx from 'ts-pkg';`
	got := specs(Scan(src))
	want := []string{"import:./types", "export-from:./bar", "import:ts-pkg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDynamicNonLiteral(t *testing.T) {
	recs := Scan("const m = await import(pathVar);\nconst n = require(dyn);")
	if len(recs) != 2 || !recs[0].Dynamic || !recs[1].Dynamic {
		t.Fatalf("expected two dynamic records, got %+v", recs)
	}
}

func TestLineNumbers(t *testing.T) {
	recs := Scan("\n\nimport a from 'a';\n\nimport b from 'b';\n")
	if recs[0].Line != 3 || recs[1].Line != 5 {
		t.Fatalf("lines %d %d", recs[0].Line, recs[1].Line)
	}
}

func TestExportLocalNotModule(t *testing.T) {
	src := `export default function App() {}
export const x = 1;
export { y };
import z from 'z';`
	got := specs(Scan(src))
	if !reflect.DeepEqual(got, []string{"import:z"}) {
		t.Fatalf("got %v", got)
	}
}

func TestLocalExportDeclarations(t *testing.T) {
	src := `export const a = 1, b = 2;
export let c;
export var d;
export function foo() {}
export async function bar() {}
export function* gen() {}
export class Baz {}
export default function () {}
export default class Qux {}
export default someExpression;
export { x, y as z };
export { a as default };
`
	res := ScanResult(src)
	got := exportSpecs(res.Exports)
	want := []string{
		"const:a", "const:b",
		"let:c",
		"var:d",
		"function:foo",
		"function:bar",
		"function:gen",
		"class:Baz",
		"default:default",
		"default:default",
		"default:default",
		"named:x", "named:z",
		"named:default",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exports got %v want %v", got, want)
	}
	wantLines := []int{1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 11, 12}
	if got := exportLines(res.Exports); !reflect.DeepEqual(got, wantLines) {
		t.Fatalf("lines got %v want %v", got, wantLines)
	}
	if len(res.Records) != 0 {
		t.Fatalf("records got %v", specs(res.Records))
	}
}

func TestReExportsAreRecordsAndExports(t *testing.T) {
	src := `export * from './other';
export * as ns from './other2';
export { a, b as c } from './other3';
export type { Foo as Bar } from './types';
`
	res := ScanResult(src)
	gotRecords := specs(res.Records)
	wantRecords := []string{
		"export-from:./other",
		"export-from:./other2",
		"export-from:./other3",
		"export-from:./types",
	}
	if !reflect.DeepEqual(gotRecords, wantRecords) {
		t.Fatalf("records got %v want %v", gotRecords, wantRecords)
	}
	gotExports := exportSpecs(res.Exports)
	wantExports := []string{"re-export:ns", "re-export:a", "re-export:c", "re-export:Bar"}
	if !reflect.DeepEqual(gotExports, wantExports) {
		t.Fatalf("exports got %v want %v", gotExports, wantExports)
	}
	wantLines := []int{2, 3, 3, 4}
	if got := exportLines(res.Exports); !reflect.DeepEqual(got, wantLines) {
		t.Fatalf("lines got %v want %v", got, wantLines)
	}
}

func TestTypeScriptLocalExports(t *testing.T) {
	src := `export type Foo = { a: string };
export interface Bar {}
export enum E {}
export declare const x: number;
export abstract class A {}
`
	res := ScanResult(src)
	got := exportSpecs(res.Exports)
	want := []string{"type:Foo", "interface:Bar", "enum:E", "const:x", "class:A"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exports got %v want %v", got, want)
	}
	wantLines := []int{1, 2, 3, 4, 5}
	if got := exportLines(res.Exports); !reflect.DeepEqual(got, wantLines) {
		t.Fatalf("lines got %v want %v", got, wantLines)
	}
}

func TestDestructuredExportDeclarations(t *testing.T) {
	src := `export const { a, b: c, d = 1, e: { f }, ...rest } = obj;
export const [x, , y, { z }] = arr;
`
	res := ScanResult(src)
	got := exportSpecs(res.Exports)
	want := []string{"const:a", "const:c", "const:d", "const:f", "const:rest", "const:x", "const:y", "const:z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exports got %v want %v", got, want)
	}
	wantLines := []int{1, 1, 1, 1, 1, 2, 2, 2}
	if got := exportLines(res.Exports); !reflect.DeepEqual(got, wantLines) {
		t.Fatalf("lines got %v want %v", got, wantLines)
	}
}

func TestCommonJSExports(t *testing.T) {
	src := `exports.foo = 1;
module.exports.bar = 2;
exports["baz"] = 3;
module.exports = { alpha, beta: value, "gamma-delta": other };
module.exports = makeThing();
`
	res := ScanResult(src)
	got := exportSpecs(res.Exports)
	want := []string{
		"commonjs:foo",
		"commonjs:bar",
		"commonjs:baz",
		"commonjs:alpha",
		"commonjs:beta",
		"commonjs:gamma-delta",
		"commonjs:module.exports",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exports got %v want %v", got, want)
	}
	wantLines := []int{1, 2, 3, 4, 4, 4, 5}
	if got := exportLines(res.Exports); !reflect.DeepEqual(got, wantLines) {
		t.Fatalf("lines got %v want %v", got, wantLines)
	}
}

func TestExportNegatives(t *testing.T) {
	src := "const s = \"export const ghost = 1\";\n" +
		"// export const comment = 1\n" +
		"/* export const block = 1 */\n" +
		"const t = `export const templ = 1`;\n" +
		"const re = /export const regex = 1/;\n" +
		"const div = <p>export const jsx = 1; it's fine</p>;\n" +
		"const exportFoo = 1, myexport = 2, _export = 3;\n" +
		"export const real = 1;\n"
	res := ScanResult(src)
	got := exportSpecs(res.Exports)
	want := []string{"const:real"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exports got %v want %v", got, want)
	}
	if res.Exports[0].Line != 8 {
		t.Fatalf("line got %d want 8", res.Exports[0].Line)
	}
}

func TestJSXHeavyExports(t *testing.T) {
	src := "const message = <p>it's export default fine</p>;\n" +
		"const view = <Comp render={() => exports.inner = 1} />;\n" +
		"export const afterJSX = 1;\n"
	res := ScanResult(src)
	got := exportSpecs(res.Exports)
	want := []string{"commonjs:inner", "const:afterJSX"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exports got %v want %v", got, want)
	}
	wantLines := []int{2, 3}
	if got := exportLines(res.Exports); !reflect.DeepEqual(got, wantLines) {
		t.Fatalf("lines got %v want %v", got, wantLines)
	}
}

func TestCRLFExportLines(t *testing.T) {
	src := "export const a = 1;\r\n" +
		"export {\r\n" +
		"  a as b\r\n" +
		"};\r\n"
	res := ScanResult(src)
	got := exportSpecs(res.Exports)
	want := []string{"const:a", "named:b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exports got %v want %v", got, want)
	}
	wantLines := []int{1, 3}
	if got := exportLines(res.Exports); !reflect.DeepEqual(got, wantLines) {
		t.Fatalf("lines got %v want %v", got, wantLines)
	}
}

func TestTemplateSubstitutionsAreScanned(t *testing.T) {
	src := "const page = `${await import('./page.js')}`;\n" +
		"const other = `prefix ${require('pkg')} suffix`;\n" +
		"const ignored = `import('literal-only') require('literal-only-2')`;\n" +
		"const nested = `${tag(`${import('nested-page')}`)}`;\n" +
		"const complex = `${(() => { const s = \"require('ghost-string')\"; /* import('ghost-comment') */ return /import('ghost-regex')/.test(x) ? import('real-complex') : null })()}`;\n"
	recs := Scan(src)
	got := specs(recs)
	want := []string{
		"dynamic-import:./page.js",
		"require:pkg",
		"dynamic-import:nested-page",
		"dynamic-import:real-complex",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	wantLines := []int{1, 2, 4, 5}
	for i, line := range wantLines {
		if recs[i].Line != line {
			t.Fatalf("record %d line got %d want %d", i, recs[i].Line, line)
		}
	}
}

func TestJSXTextTagsAndAttributesAreNotExecutable(t *testing.T) {
	src := "const message = <p>import 'ghost-package'</p>;\n" +
		"const m2 = <p>it's fine</p>; import React from 'react';\n" +
		"const handlers = { import(path) {} };\n" +
		"const attrs = <Comp title=\"import 'ghost-attr'\" data={`require('ghost-template')`} onClick={() => require('handler-pkg')} />;\n"
	recs := Scan(src)
	got := specs(recs)
	want := []string{"import:react", "require:handler-pkg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if recs[0].Line != 2 || recs[1].Line != 4 {
		t.Fatalf("lines got %d and %d", recs[0].Line, recs[1].Line)
	}
}

func TestRegexContextDoesNotInventImports(t *testing.T) {
	src := "function f(value) {\n" +
		"  return /import('ghost-return')/.test(value);\n" +
		"  if (value) /require('ghost-if')/.test(value);\n" +
		"  if (value) {}/import('ghost-block')/.test(value);\n" +
		"  const div = value / require('real-divisor');\n" +
		"  const afterParen = (value) / require('real-after-paren');\n" +
		"  const afterObject = { value } / require('real-after-object');\n" +
		"}\n"
	recs := Scan(src)
	got := specs(recs)
	want := []string{
		"require:real-divisor",
		"require:real-after-paren",
		"require:real-after-object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if recs[0].Line != 5 || recs[1].Line != 6 || recs[2].Line != 7 {
		t.Fatalf("lines got %d, %d, %d", recs[0].Line, recs[1].Line, recs[2].Line)
	}
}

func TestCRLFMultilineImports(t *testing.T) {
	src := "import {\r\n" +
		"  foo\r\n" +
		"}\r\n" +
		"from 'pkg';\r\n" +
		"import Foo\r\n" +
		"  from 'pkg2';\r\n"
	recs := Scan(src)
	got := specs(recs)
	want := []string{"import:pkg", "import:pkg2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if recs[0].Line != 1 || recs[1].Line != 5 {
		t.Fatalf("lines got %d and %d", recs[0].Line, recs[1].Line)
	}
}

func TestRequireResolve(t *testing.T) {
	src := "const workerPath = require.resolve('some-worker');\n" +
		"const spaced = require . resolve ( 'spaced-worker' );\n" +
		"const cache = require.cache;\n" +
		"const main = require.main;\n"
	recs := Scan(src)
	got := specs(recs)
	want := []string{"require:some-worker", "require:spaced-worker"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if recs[0].Line != 1 || recs[1].Line != 2 {
		t.Fatalf("lines got %d and %d", recs[0].Line, recs[1].Line)
	}
}

func TestHasJSXDetection(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "component with jsx",
			src:  "export default function Header() {\n  return <div className=\"h\">Hi</div>;\n}\n",
			want: true,
		},
		{
			name: "plain util no jsx",
			src:  "export function add(a, b) {\n  return a + b;\n}\n",
			want: false,
		},
		{
			name: "regex and comparisons do not look like jsx",
			src:  "function f(a, b) {\n  return a < b && b > a;\n}\n",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := ScanResult(tc.src)
			if res.HasJSX != tc.want {
				t.Fatalf("HasJSX = %v, want %v", res.HasJSX, tc.want)
			}
		})
	}
}
