package main

import (
	"github.com/frrad/mooo/internal/client"
	"testing"
)

func TestOperatorListingReportsShadowDiagnosticsWithoutRemotePanic(t *testing.T) {
	mode, err := labShadowMode([]string{"chats", "list"}, "")
	if err != nil || mode != client.BSONShadowLog {
		t.Fatalf("operator mode=%v error=%v", mode, err)
	}
	for _, configured := range []string{"panic", "off", "log"} {
		mode, err := labShadowMode([]string{"chats", "list"}, configured)
		want, _ := client.ParseBSONShadowMode(configured)
		if err != nil || mode != want {
			t.Fatal("explicit diagnostic mode overridden")
		}
	}
	mode, err = labShadowMode([]string{"auth", "inspect"}, "")
	if err != nil || mode != client.BSONShadowPanic {
		t.Fatal("research default weakened")
	}
}

func TestOperatorContactPhotoReportsShadowDiagnostics(t *testing.T) {
	mode, err := labShadowMode([]string{"contacts", "photo"}, "")
	if err != nil || mode != client.BSONShadowLog {
		t.Fatalf("photo mode=%v error=%v", mode, err)
	}
	mode, err = labShadowMode([]string{"contacts", "photo"}, "panic")
	if err != nil || mode != client.BSONShadowPanic {
		t.Fatal("explicit diagnostic mode overridden")
	}
}
