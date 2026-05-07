package utils

import "testing"

func TestParseFrontmatter(t *testing.T) {
	values, body, ok := ParseFrontmatter("---\nname: demo\ndescription: hello: world\n\n---\nbody\n")
	if !ok {
		t.Fatal("ParseFrontmatter ok = false")
	}
	if values["name"] != "demo" {
		t.Fatalf("name = %q", values["name"])
	}
	if values["description"] != "hello: world" {
		t.Fatalf("description = %q", values["description"])
	}
	if body != "body" {
		t.Fatalf("body = %q", body)
	}
}

func TestParseFrontmatterNoMarker(t *testing.T) {
	values, body, ok := ParseFrontmatter("body")
	if ok {
		t.Fatal("ParseFrontmatter ok = true")
	}
	if values != nil {
		t.Fatalf("values = %#v", values)
	}
	if body != "body" {
		t.Fatalf("body = %q", body)
	}
}

func TestCopyMap(t *testing.T) {
	in := map[string]string{"a": "b"}
	out := CopyMap(in)
	if out["a"] != "b" {
		t.Fatalf("out[a] = %q", out["a"])
	}
	out["a"] = "c"
	if in["a"] != "b" {
		t.Fatalf("input changed to %q", in["a"])
	}
}
