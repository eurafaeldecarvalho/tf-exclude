package limit

import (
	"strings"
	"testing"
)

func TestSplitCoversEveryTarget(t *testing.T) {
	var targets []string
	for i := 0; i < 50; i++ {
		targets = append(targets, "terraform_data.keep["+itoa(i)+"]")
	}
	base := []string{"-chdir=dir", "plan", "-input=false", "-out=plan.tfplan"}
	batches, err := Split("terraform", base, targets, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) < 2 {
		t.Fatalf("expected multiple batches, got %d", len(batches))
	}
	var got []string
	for i, b := range batches {
		n := 0
		for _, a := range b {
			if strings.HasPrefix(a, "-target=") {
				got = append(got, strings.TrimPrefix(a, "-target="))
				n++
			}
		}
		if n == 0 {
			t.Fatalf("batch %d has no targets: %v", i, b)
		}
	}
	if strings.Join(got, ",") != strings.Join(targets, ",") {
		t.Fatalf("coverage mismatch:\n got %v\nwant %v", got, targets)
	}
}

func TestSplitSingleTargetTooLarge(t *testing.T) {
	_, err := Split("terraform", []string{"plan"}, []string{strings.Repeat("a", 500)}, 80)
	if err == nil {
		t.Fatal("expected error")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
