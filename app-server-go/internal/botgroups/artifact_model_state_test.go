package botgroups

import (
	"encoding/json"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/artifacts"
)

func TestArtifactModelDeduplicatesCompleteNativeEvidenceAcrossOutputs(t *testing.T) {
	source := NativeScriptChange{ID: "patch", Path: "/work/generate.py", Diff: strings.Repeat("complete native script content\n", 250)}
	op := NativeFileOperation{ID: "run", Kind: "commandExecution", Command: "python generate.py", ScriptSources: []NativeScriptChange{source}}
	input := ArtifactInput{Request: "deliver generated files", Reply: "generated outputs"}
	for _, name := range []string{"report.docx", "page.html", "image.png"} {
		input.Candidates = append(input.Candidates, ArtifactCandidate{File: artifacts.File{ID: name, Path: "/work/" + name}, Operations: []NativeFileOperation{op}})
	}
	state := modelArtifactState(input)
	if len(state.Candidates) != 3 || len(state.Operations) != 1 || len(state.ScriptChanges) != 1 || state.ScriptChanges["script_0"].Diff != source.Diff {
		t.Fatalf("native evidence was duplicated or truncated: %+v", state)
	}
	for _, candidate := range state.Candidates {
		if len(candidate.OperationIDs) != 1 || len(state.Operations[candidate.OperationIDs[0]].ScriptSourceIDs) != 1 {
			t.Fatal("output lost its shared execution evidence")
		}
	}
	body, _ := json.Marshal(state)
	if strings.Count(string(body), "complete native script content") != 250 {
		t.Fatal("full native script was repeated per output")
	}
	if len(artifactBatches(input)) != 1 {
		t.Fatal("shared evidence was not classified together")
	}
}

func TestArtifactModelSplitsIndependentLargeEvidenceWithoutDroppingFiles(t *testing.T) {
	input := ArtifactInput{Request: "deliver both files", Reply: "ready"}
	for _, name := range []string{"one", "two", "three"} {
		input.Candidates = append(input.Candidates, ArtifactCandidate{
			File: artifacts.File{ID: name, Path: "/work/" + name + ".png"},
			Operations: []NativeFileOperation{{ID: name + "-run", Kind: "commandExecution", Command: "python " + name + ".py",
				ScriptSources: []NativeScriptChange{{ID: name + "-patch", Path: "/work/" + name + ".py", Diff: strings.Repeat("x", 15000)}}}},
		})
	}
	batches := artifactBatches(input)
	if len(batches) != 3 {
		t.Fatalf("large independent evidence was not split: %d", len(batches))
	}
	for i, batch := range batches {
		if len(batch.Candidates) != 1 || batch.Candidates[0].File.ID != input.Candidates[i].File.ID ||
			batch.Candidates[0].Operations[0].ScriptSources[0].Diff != input.Candidates[i].Operations[0].ScriptSources[0].Diff {
			t.Fatal("batching lost a file or changed native evidence")
		}
	}
}
