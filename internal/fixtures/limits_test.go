package fixtures

import (
	"strings"
	"testing"
)

func TestCheckIntegers(t *testing.T) {
	f := decodeEmbedded(t)
	if err := CheckIntegers(f); err != nil {
		t.Fatalf("the fixtures hold an integer above MaxInteger: %v", err)
	}

	// revisions[36], rev_aug26_2, is the last revision of its dataset, so a
	// higher sequence keeps every invariant.
	f.Revisions[36].Sequence = MaxInteger
	if err := CheckIntegers(f); err != nil {
		t.Errorf("sequence MaxInteger: %v", err)
	}
	f.Revisions[36].Sequence = MaxInteger + 1
	if vs := Check(f); len(vs) > 0 {
		t.Fatalf("the altered fixtures break invariants:\n%v", vs)
	}
	err := CheckIntegers(f)
	want := `revisions.json: revisions[36] (revision_id "rev_aug26_2"): spec/api.md keeps integers below 2^53: sequence 9007199254740992 is above 9007199254740991`
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}

	// Every such sequence is reported, a line each.
	f.Revisions[35].Sequence = MaxInteger + 1
	if err := CheckIntegers(f); err == nil || len(strings.Split(err.Error(), "\n")) != 2 {
		t.Errorf("two sequences above MaxInteger: %v", err)
	}
}
