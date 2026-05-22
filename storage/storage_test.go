package storage

import "testing"

func TestApplyCountMutationSkipsUnlearnMiss(t *testing.T) {
	mutation := applyCountMutation(TokenCount{}, false, 3, true, false)

	if mutation.Upsert || mutation.Delete {
		t.Fatalf("unlearn miss should not write or delete: %+v", mutation)
	}
}

func TestApplyCountMutationDeletesExistingZeroCountRow(t *testing.T) {
	mutation := applyCountMutation(TokenCount{CountSpam: 2}, true, 2, true, false)

	if !mutation.Delete || mutation.Upsert {
		t.Fatalf("expected delete without upsert: %+v", mutation)
	}
	if mutation.Count != (TokenCount{}) {
		t.Fatalf("expected zero counts after exact unlearn, got %+v", mutation.Count)
	}
}

func TestApplyCountMutationKeepsRowsWithRemainingCounts(t *testing.T) {
	mutation := applyCountMutation(TokenCount{CountHam: 3, CountSpam: 1}, true, 2, false, false)

	if !mutation.Upsert || mutation.Delete {
		t.Fatalf("expected update without delete: %+v", mutation)
	}
	if mutation.Count != (TokenCount{CountHam: 1, CountSpam: 1}) {
		t.Fatalf("unexpected counts: %+v", mutation.Count)
	}
}

func TestApplyCountMutationInsertsLearnMiss(t *testing.T) {
	mutation := applyCountMutation(TokenCount{}, false, 4, true, true)

	if !mutation.Upsert || mutation.Delete {
		t.Fatalf("learn miss should insert/update: %+v", mutation)
	}
	if mutation.Count != (TokenCount{CountSpam: 4}) {
		t.Fatalf("unexpected counts: %+v", mutation.Count)
	}
}
