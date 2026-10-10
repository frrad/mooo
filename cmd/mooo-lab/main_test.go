package main

import (
	"testing"

	"github.com/frrad/mooo/internal/shadow"
)

func TestOperatorListingReportsShadowDiagnosticsWithoutRemotePanic(t *testing.T) {
	mode, err := labShadowMode([]string{"chats", "list"}, "")
	if err != nil || mode != shadow.ModeLog {
		t.Fatalf("operator mode=%v error=%v", mode, err)
	}
	for _, configured := range []string{"panic", "off", "log"} {
		mode, err := labShadowMode([]string{"chats", "list"}, configured)
		want, _ := shadow.ParseMode(configured)
		if err != nil || mode != want {
			t.Fatal("explicit diagnostic mode overridden")
		}
	}
	mode, err = labShadowMode([]string{"auth", "inspect"}, "")
	if err != nil || mode != shadow.ModePanic {
		t.Fatal("research default weakened")
	}
	if _, err := labShadowMode([]string{"chats", "list"}, "loud"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestOperatorContactPhotoReportsShadowDiagnostics(t *testing.T) {
	mode, err := labShadowMode([]string{"contacts", "photo"}, "")
	if err != nil || mode != shadow.ModeLog {
		t.Fatalf("photo mode=%v error=%v", mode, err)
	}
	mode, err = labShadowMode([]string{"contacts", "photo"}, "panic")
	if err != nil || mode != shadow.ModePanic {
		t.Fatal("explicit diagnostic mode overridden")
	}
}
