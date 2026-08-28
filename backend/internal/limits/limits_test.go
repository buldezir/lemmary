package limits

import (
	"math"
	"testing"
)

func TestEnvLimitParsing(t *testing.T) {
	cases := []struct {
		name          string
		raw           string
		set           bool
		wantUnlimited bool
		wantValue     int64
	}{
		{name: "unset", set: false, wantUnlimited: true},
		{name: "empty", raw: "", set: true, wantUnlimited: true},
		{name: "whitespace", raw: "   ", set: true, wantUnlimited: true},
		{name: "positive", raw: "25", set: true, wantValue: 25},
		{name: "padded", raw: " 25 ", set: true, wantValue: 25},
		// Zero is a limit a plan sells ("no extra accounts"), not a way of
		// saying unset, so it has to survive parsing as a real bound.
		{name: "explicit zero", raw: "0", set: true, wantValue: 0},
		// A typo grants room rather than taking the instance down.
		{name: "not a number", raw: "2O", set: true, wantUnlimited: true},
		{name: "float", raw: "2.5", set: true, wantUnlimited: true},
		{name: "negative", raw: "-1", set: true, wantUnlimited: true},
		{name: "max int64 is clamped", raw: "9223372036854775807", set: true, wantValue: math.MaxInt64 - 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(EnvDocuments, tc.raw)
			}
			got, _ := envLimit(nil, EnvDocuments)
			if got.IsUnlimited() != tc.wantUnlimited {
				t.Fatalf("%q: IsUnlimited = %v, want %v", tc.raw, got.IsUnlimited(), tc.wantUnlimited)
			}
			if !tc.wantUnlimited && got.Value() != tc.wantValue {
				t.Fatalf("%q: Value = %d, want %d", tc.raw, got.Value(), tc.wantValue)
			}
		})
	}
}

func TestFromEnvReadsEveryVariable(t *testing.T) {
	t.Setenv(EnvDocuments, "1")
	t.Setenv(EnvDocumentPages, "2")
	t.Setenv(EnvStorageBytes, "3")
	t.Setenv(EnvFileBytes, "4")
	t.Setenv(EnvFilePages, "5")
	t.Setenv(EnvAdditionalUsers, "6")

	lim, misconfigured := FromEnv(nil)
	if len(misconfigured) != 0 {
		t.Fatalf("usable values reported as misconfigured: %v", misconfigured)
	}
	for _, tc := range []struct {
		name  string
		limit Limit
		want  int64
	}{
		{EnvDocuments, lim.Documents, 1},
		{EnvDocumentPages, lim.DocumentPages, 2},
		{EnvStorageBytes, lim.StorageBytes, 3},
		{EnvFileBytes, lim.FileBytes, 4},
		{EnvFilePages, lim.FilePages, 5},
		{EnvAdditionalUsers, lim.AdditionalUsers, 6},
	} {
		if tc.limit.IsUnlimited() {
			t.Fatalf("%s: unlimited, want %d", tc.name, tc.want)
		}
		if tc.limit.Value() != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, tc.limit.Value(), tc.want)
		}
	}
}

// The default has to be "nothing is bounded" or an existing self-hosted install
// changes behaviour on upgrade.
func TestZeroLimitsBoundNothing(t *testing.T) {
	var lim Limits
	if lim.Any() {
		t.Fatal("the zero Limits bounds something")
	}
	if err := lim.CheckFile(1<<40, 100000); err != nil {
		t.Fatalf("unlimited CheckFile refused: %v", err)
	}
	err := lim.CheckRoom(Usage{Documents: 1e9, DocumentPages: 1e9, StorageBytes: 1e15}, 1, 1, 1)
	if err != nil {
		t.Fatalf("unlimited CheckRoom refused: %v", err)
	}
	if err := lim.CheckAdditionalUsers(1e6); err != nil {
		t.Fatalf("unlimited CheckAdditionalUsers refused: %v", err)
	}
}

func TestFromEnvUnsetIsUnlimited(t *testing.T) {
	for _, key := range EnvKeys() {
		t.Setenv(key, "")
	}
	lim, misconfigured := FromEnv(nil)
	if lim.Any() {
		t.Fatal("no variables set, but limits report as enforced")
	}
	// Unset is a deliberate "unlimited", not a mistake to report.
	if len(misconfigured) != 0 {
		t.Fatalf("unset variables reported as misconfigured: %v", misconfigured)
	}
}

// A value nobody can read falls back to unlimited, which is a working instance
// on the wrong plan. The name has to come back so it can be logged loudly and
// shown to an admin.
func TestFromEnvReportsUnusableValues(t *testing.T) {
	t.Setenv(EnvDocuments, "2O")
	t.Setenv(EnvFilePages, "-3")
	t.Setenv(EnvStorageBytes, "1000")

	lim, misconfigured := FromEnv(nil)
	if !lim.Documents.IsUnlimited() || !lim.FilePages.IsUnlimited() {
		t.Fatal("an unusable value produced a bound")
	}
	if lim.StorageBytes.IsUnlimited() {
		t.Fatal("a usable value alongside bad ones was dropped")
	}
	want := map[string]bool{EnvDocuments: true, EnvFilePages: true}
	if len(misconfigured) != len(want) {
		t.Fatalf("misconfigured = %v, want the two unusable keys", misconfigured)
	}
	for _, key := range misconfigured {
		if !want[key] {
			t.Fatalf("misconfigured names %q, which was usable", key)
		}
	}
}

func TestLimitRemaining(t *testing.T) {
	limit := Of(10)
	for _, tc := range []struct{ used, want int64 }{
		{0, 10}, {3, 7}, {10, 0},
		// Already over: report no headroom rather than a negative.
		{12, 0},
	} {
		if got := limit.Remaining(tc.used); got != tc.want {
			t.Fatalf("Remaining(%d) = %d, want %d", tc.used, got, tc.want)
		}
	}
}

func TestLimitExceeded(t *testing.T) {
	limit := Of(2)
	if limit.Exceeded(2) {
		t.Fatal("2 exceeds a limit of 2")
	}
	if !limit.Exceeded(3) {
		t.Fatal("3 does not exceed a limit of 2")
	}
	if Unlimited().Exceeded(math.MaxInt64) {
		t.Fatal("an unlimited limit was exceeded")
	}
}

func TestAnyReportsEachLimit(t *testing.T) {
	for name, lim := range map[string]Limits{
		"documents":        {Documents: Of(1)},
		"document pages":   {DocumentPages: Of(1)},
		"storage bytes":    {StorageBytes: Of(1)},
		"file bytes":       {FileBytes: Of(1)},
		"file pages":       {FilePages: Of(1)},
		"additional users": {AdditionalUsers: Of(0)},
	} {
		if !lim.Any() {
			t.Fatalf("%s set, but Any() is false", name)
		}
	}
}
