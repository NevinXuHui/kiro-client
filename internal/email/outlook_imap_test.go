package email

import (
	"strings"
	"testing"
)

func TestParseOutlookLineSwappedClientAndRefresh(t *testing.T) {
	t.Parallel()
	cid := "8b4ba9dd-3ea5-4e5f-86f1-ddba2230dcf2"
	rt := "M.C525_BAY.0.U.-" + strings.Repeat("x", 80)
	normal := ParseOutlookLines("a@outlook.com----pw----" + cid + "----" + rt)
	swapped := ParseOutlookLines("a@outlook.com----pw----" + rt + "----" + cid)
	if len(normal) != 1 || len(swapped) != 1 {
		t.Fatalf("parse count normal=%d swapped=%d", len(normal), len(swapped))
	}
	if normal[0].ClientID != cid || normal[0].RefreshToken != rt {
		t.Fatalf("normal order: %+v", normal[0])
	}
	if swapped[0].ClientID != cid || swapped[0].RefreshToken != rt {
		t.Fatalf("swapped order: %+v", swapped[0])
	}
}

func TestParseOutlookLineFormats(t *testing.T) {
	t.Parallel()

	four := "a@outlook.com----pw----cid----rtoken"
	five := "a@outlook.com----pw----cid----rtoken----atok"
	got := ParseOutlookLines(four + "\n" + five)
	if len(got) != 2 {
		t.Fatalf("got %d accounts, want 2", len(got))
	}
	if got[0].Email != "a@outlook.com" || got[0].ClientID != "cid" || got[0].RefreshToken != "rtoken" {
		t.Fatalf("4-field parse mismatch: %+v", got[0])
	}
	if got[1].RefreshToken != "rtoken" {
		t.Fatalf("5-field refresh token polluted: %q", got[1].RefreshToken)
	}
	if got[1].AccessToken != "atok" {
		t.Fatalf("5-field access token = %q", got[1].AccessToken)
	}
}

func TestParseOutlookLineRejectsShort(t *testing.T) {
	t.Parallel()
	if got := ParseOutlookLines("a@outlook.com----onlypw"); len(got) != 0 {
		t.Fatalf("2-field should be rejected, got %+v", got)
	}
}

func TestParseOutlookLineSpaceSeparated(t *testing.T) {
	t.Parallel()
	data := "a@outlook.com----p1----c1----r1----t1 b@hotmail.com----p2----c2----r2"
	got := ParseOutlookLines(data)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(got), got)
	}
	if got[0].AccessToken != "t1" || got[1].Email != "b@hotmail.com" {
		t.Fatalf("space-separated parse mismatch: %+v", got)
	}
}

func TestParseOutlookOAuth2String(t *testing.T) {
	t.Parallel()
	raw := "user=a@outlook.com\x01auth=Bearer tok\x01\x01"
	enc := buildXOAuth2("a@outlook.com", "tok")
	if enc == "" || strings.Contains(enc, "user=") {
		t.Fatalf("XOAUTH2 should be base64, got %q", enc)
	}
	_ = raw
}
