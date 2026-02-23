package text

import "testing"

func TestSpanValidate(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		span  Span
		valid bool
	}{
		"valid":                  {span: Span{Start: 0, End: 1}, valid: true},
		"empty valid":            {span: Span{Start: 3, End: 3}, valid: true},
		"negative start invalid": {span: Span{Start: -1, End: 1}, valid: false},
		"negative end invalid":   {span: Span{Start: 0, End: -1}, valid: false},
		"end before start":       {span: Span{Start: 5, End: 4}, valid: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.span.IsValid(); got != tc.valid {
				t.Fatalf("IsValid() = %v, want %v", got, tc.valid)
			}
			err := tc.span.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("Validate() error = %v, valid=%v", err, tc.valid)
			}
		})
	}
}

func TestNewSpan(t *testing.T) {
	t.Parallel()

	if _, err := NewSpan(2, 1); err == nil {
		t.Fatal("NewSpan(2,1) expected error")
	}

	s, err := NewSpan(2, 5)
	if err != nil {
		t.Fatalf("NewSpan(2,5) error = %v", err)
	}
	if s.Start != 2 || s.End != 5 {
		t.Fatalf("NewSpan(2,5) = %+v, want [2,5)", s)
	}
}

func TestSpanContainsHalfOpen(t *testing.T) {
	t.Parallel()

	s := Span{Start: 2, End: 5} // [2,5)
	if !s.Contains(2) {
		t.Fatal("expected start boundary to be included")
	}
	if !s.Contains(4) {
		t.Fatal("expected interior offset to be included")
	}
	if s.Contains(5) {
		t.Fatal("expected end boundary to be excluded")
	}
	if s.Contains(1) {
		t.Fatal("expected offset before start to be excluded")
	}
}

func TestSpanEmptyContainsNothing(t *testing.T) {
	t.Parallel()

	s := Span{Start: 7, End: 7}
	if !s.IsEmpty() {
		t.Fatal("expected empty span")
	}
	if s.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", s.Len())
	}
	if s.Contains(7) {
		t.Fatal("empty span should not contain offsets")
	}
}

func TestSpanContainsSpanAndIntersectsHalfOpen(t *testing.T) {
	t.Parallel()

	base := Span{Start: 10, End: 20}
	inside := Span{Start: 12, End: 18}
	touchLeft := Span{Start: 5, End: 10}
	touchRight := Span{Start: 20, End: 25}
	overlap := Span{Start: 19, End: 25}

	if !base.ContainsSpan(inside) {
		t.Fatal("expected contained span")
	}
	if base.ContainsSpan(touchLeft) {
		t.Fatal("did not expect touchLeft to be contained")
	}
	if base.Intersects(touchLeft) {
		t.Fatal("touching left boundary should not intersect for half-open spans")
	}
	if base.Intersects(touchRight) {
		t.Fatal("touching right boundary should not intersect for half-open spans")
	}
	if !base.Intersects(overlap) {
		t.Fatal("expected overlapping span to intersect")
	}
}
