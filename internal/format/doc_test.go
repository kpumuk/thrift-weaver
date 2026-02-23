package format

import "testing"

func TestRenderSoftLineWrapsByWidth(t *testing.T) {
	t.Parallel()

	doc := Group(Concat(Text("a"), SoftLine(), Text("b")))

	gotWide, err := Render(doc, RenderOptions{LineWidth: 10})
	if err != nil {
		t.Fatalf("Render wide: %v", err)
	}
	if string(gotWide) != "a b" {
		t.Fatalf("wide render = %q, want %q", gotWide, "a b")
	}

	gotNarrow, err := Render(doc, RenderOptions{LineWidth: 1})
	if err != nil {
		t.Fatalf("Render narrow: %v", err)
	}
	if string(gotNarrow) != "a\nb" {
		t.Fatalf("narrow render = %q, want %q", gotNarrow, "a\nb")
	}
}

func TestRenderIndentAndDeterminism(t *testing.T) {
	t.Parallel()

	doc := Group(Concat(
		Text("{"),
		Indent(Concat(
			Line(),
			Text("alpha"),
			Line(),
			Group(Concat(Text("beta"), SoftLine(), Text("gamma"))),
		)),
		Line(),
		Text("}"),
	))

	opts := RenderOptions{LineWidth: 6, Indent: "  ", Newline: "\n"}
	got1, err := Render(doc, opts)
	if err != nil {
		t.Fatalf("Render #1: %v", err)
	}
	got2, err := Render(doc, opts)
	if err != nil {
		t.Fatalf("Render #2: %v", err)
	}
	if string(got1) != string(got2) {
		t.Fatalf("render not deterministic: %q vs %q", got1, got2)
	}

	want := "{\n  alpha\n  beta gamma\n}"
	if string(got1) != want {
		t.Fatalf("render = %q, want %q", got1, want)
	}
}
