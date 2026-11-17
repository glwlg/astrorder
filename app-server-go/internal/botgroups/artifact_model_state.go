package botgroups

import (
	"encoding/json"
	"fmt"

	"astrorder.dev/app-server/internal/artifacts"
)

// Native scripts and commands are shared by several outputs. Send their
// immutable records once, with references, instead of repeating full diffs.
type artifactModelState struct {
	Request       string                            `json:"request"`
	Reply         string                            `json:"reply"`
	Candidates    []artifactModelCandidate          `json:"candidates"`
	Operations    map[string]artifactModelOperation `json:"operations"`
	ScriptChanges map[string]NativeScriptChange     `json:"script_changes"`
}

type artifactModelCandidate struct {
	File         artifacts.File `json:"file"`
	OperationIDs []string       `json:"operation_ids"`
}

type artifactModelOperation struct {
	NativeFileOperation
	ScriptSourceIDs []string `json:"script_source_ids,omitempty"`
}

func modelArtifactState(input ArtifactInput) artifactModelState {
	state := artifactModelState{
		Request: input.Request, Reply: input.Reply, Candidates: []artifactModelCandidate{},
		Operations: map[string]artifactModelOperation{}, ScriptChanges: map[string]NativeScriptChange{},
	}
	opKeys, sourceKeys := map[string]string{}, map[string]string{}
	for _, candidate := range input.Candidates {
		item := artifactModelCandidate{File: candidate.File, OperationIDs: []string{}}
		for _, operation := range candidate.Operations {
			identity := operation.ID + "\x00" + operation.Path
			key, exists := opKeys[identity]
			if !exists {
				key = fmt.Sprintf("op_%d", len(opKeys))
				opKeys[identity] = key
			}
			value, known := state.Operations[key]
			if !known {
				copy := operation
				copy.ScriptSources = nil
				value = artifactModelOperation{NativeFileOperation: copy}
			}
			// Different outputs may carry different relevant patches of the
			// same executed script. Union the actual source records.
			for _, source := range operation.ScriptSources {
				identity := source.ID + "\x00" + source.Path
				sourceKey, exists := sourceKeys[identity]
				if !exists {
					sourceKey = fmt.Sprintf("script_%d", len(sourceKeys))
					sourceKeys[identity] = sourceKey
					state.ScriptChanges[sourceKey] = source
				}
				present := false
				for _, old := range value.ScriptSourceIDs {
					present = present || old == sourceKey
				}
				if !present {
					value.ScriptSourceIDs = append(value.ScriptSourceIDs, sourceKey)
				}
			}
			state.Operations[key] = value
			item.OperationIDs = append(item.OperationIDs, key)
		}
		state.Candidates = append(state.Candidates, item)
	}
	return state
}

const artifactBatchBytes = 24 * 1024

func artifactBatches(input ArtifactInput) []ArtifactInput {
	batches := []ArtifactInput{}
	current := ArtifactInput{Request: input.Request, Reply: input.Reply}
	for _, candidate := range input.Candidates {
		trial := current
		trial.Candidates = append(append([]ArtifactCandidate{}, current.Candidates...), candidate)
		body, _ := json.Marshal(map[string]any{"state": modelArtifactState(trial), "questions": artifactQuestions(trial)})
		if len(current.Candidates) > 0 && len(body) > artifactBatchBytes {
			batches = append(batches, current)
			current = ArtifactInput{Request: input.Request, Reply: input.Reply}
		}
		current.Candidates = append(current.Candidates, candidate)
	}
	if len(current.Candidates) > 0 {
		batches = append(batches, current)
	}
	return batches
}
