package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestAdmissionJSONShapes(t *testing.T) {
	for _, decision := range []struct{ qualified, accept bool }{{true, true}, {true, false}, {false, false}} {
		object := fmt.Sprintf(`{"qualified":%t,"accept":%t,"reason":"ok"}`, decision.qualified, decision.accept)
		for _, output := range []string{object, " \n[ " + object + " ]\t "} {
			accept, qualified, err := parseAdmission(output)
			if err != nil || accept != decision.accept || qualified != decision.qualified {
				t.Fatalf("%s: %t %t %v", output, accept, qualified, err)
			}
		}
	}
	valid := `{"qualified":true,"accept":true,"reason":"ok"}`
	for _, output := range []string{
		"[]", "[null]", "null", "true", "[true]", "[{}]", "[[" + valid + "]]", "[" + valid + "," + valid + "]",
		"[" + valid + "] {}", "[" + valid + ",]", "[", valid + " {}",
		`[{"qualified":true,"accept":true}]`,
		`[{"qualified":true,"accept":"true","reason":"ok"}]`,
		`[{"qualified":false,"accept":true,"reason":"ok"}]`,
		`[{"qualified":true,"accept":true,"reason":"ok","extra":1}]`,
		`[{"qualified":true,"accept":true,"reason":" "}]`,
		fmt.Sprintf(`[{"qualified":true,"accept":true,"reason":%q}]`, strings.Repeat("中", 1001)),
	} {
		if _, _, err := parseAdmission(output); err == nil {
			t.Fatalf("accepted invalid response: %s", output)
		}
	}
}

func TestAdmissionArrayPublication(t *testing.T) {
	for _, mode := range []string{"accept", "reject", "multiple"} {
		t.Run(mode, func(t *testing.T) {
			a, c, _ := admissionApp(t, func(instruction string, _ []rankInput) string {
				if !strings.Contains(instruction, "只审核第一条候选") {
					t.Error("missing single-decision instruction")
				}
				result := fmt.Sprintf(`{"qualified":%t,"accept":%t,"reason":"review"}`, mode == "accept", mode == "accept")
				if mode == "multiple" {
					return "[" + result + "," + result + "]"
				}
				return "[" + result + "]"
			})
			old := seedAdmission(t, a, c)
			before := a.meta("rankingState")
			fresh := Meme{ID: "new/array", Channel: "memes", Text: "new array candidate"}
			if err := a.storeMeme(fresh); err != nil {
				t.Fatal(err)
			}
			err := a.rankIncremental(c, []Meme{fresh})
			if (err != nil) != (mode == "multiple") {
				t.Fatalf("unexpected error: %v", err)
			}
			got, err := a.publishedCandidates(c)
			if err != nil || len(got) != 16 {
				t.Fatalf("feed: %v %v", got, err)
			}
			if mode == "accept" {
				found := false
				for _, m := range got {
					found = found || m.ID == fresh.ID
				}
				if !found {
					t.Fatal("single array decision not published")
				}
			} else {
				for i := range old {
					if old[i].ID != got[i].ID {
						t.Fatal("rejected or invalid decision changed feed")
					}
				}
				if a.meta("lastRun") != "seed time" {
					t.Fatal("unchanged feed republished")
				}
			}
			if mode == "multiple" && a.meta("rankingState") != before {
				t.Fatal("invalid decision persisted")
			}
		})
	}
}
