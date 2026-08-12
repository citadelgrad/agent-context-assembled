package inspect

import (
	"encoding/json"
	"strings"
	"testing"
)

func FuzzReadCodexRolloutStateMachine(f *testing.F) {
	for _, seed := range [][]byte{
		{},
		{0, 1, 2, 3, 4, 5, 6},
		{1, 1, 2, 2, 0, 4},
		{3, 3, 1, 5, 2, 6},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, operations []byte) {
		if len(operations) > 128 {
			operations = operations[:128]
		}
		content, wantCWD, wantBase, wantUser := codexRolloutFixture(operations)
		cwd, base, user, ok := readCodexRolloutReader(strings.NewReader(content))
		if cwd != wantCWD || base != wantBase || user != wantUser || ok != (wantCWD != "") {
			t.Fatalf("got (%q,%q,%q,%v), want (%q,%q,%q,%v) for operations %v",
				cwd, base, user, ok, wantCWD, wantBase, wantUser, wantCWD != "", operations)
		}

		neutral := "not json\n{\"type\":\"unknown\",\"payload\":{}}\n\n" + content
		neutralCWD, neutralBase, neutralUser, neutralOK := readCodexRolloutReader(strings.NewReader(neutral))
		if neutralCWD != cwd || neutralBase != base || neutralUser != user || neutralOK != ok {
			t.Fatal("inserting irrelevant records changed accumulated state")
		}
	})
}

func codexRolloutFixture(operations []byte) (content, cwd, base, user string) {
	var lines []string
	for i, operation := range operations {
		value := string(rune('a' + i%26))
		switch operation % 7 {
		case 0:
			lines = append(lines, "")
		case 1:
			lines = append(lines, rolloutRecord("session_meta", map[string]any{
				"cwd": value, "base_instructions": map[string]any{"text": "base-" + value},
			}))
			cwd, base = value, "base-"+value
		case 2:
			lines = append(lines, rolloutRecord("turn_context", map[string]any{
				"cwd": value, "user_instructions": "user-" + value,
			}))
			cwd, user = value, "user-"+value
		case 3:
			lines = append(lines, "{malformed")
		case 4:
			lines = append(lines, rolloutRecord("unknown", map[string]any{"cwd": value}))
		case 5:
			lines = append(lines, `{"type":"session_meta","payload":"malformed-payload"}`)
		case 6:
			lines = append(lines, rolloutRecord("turn_context", map[string]any{
				"cwd": "", "user_instructions": "",
			}))
		}
	}
	return strings.Join(lines, "\n") + "\n", cwd, base, user
}

func rolloutRecord(recordType string, payload any) string {
	record, err := json.Marshal(map[string]any{"type": recordType, "payload": payload})
	if err != nil {
		panic(err)
	}
	return string(record)
}
