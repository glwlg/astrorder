package capabilities

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Hermes keeps its settings in config.yaml, which the user also edits by hand.
// Editing the node tree keeps comments and key order; only the touched keys
// change.

func yamlParse(data []byte) (*yaml.Node, *yaml.Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}, root, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("config.yaml 顶层不是映射")
	}
	return &doc, doc.Content[0], nil
}

func yamlDump(doc *yaml.Node) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func mapIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	if i := mapIndex(m, key); i >= 0 {
		return m.Content[i+1]
	}
	return nil
}

func mapSet(m *yaml.Node, key string, value any) error {
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return err
	}
	if i := mapIndex(m, key); i >= 0 {
		node.HeadComment, node.LineComment, node.FootComment = m.Content[i+1].HeadComment, m.Content[i+1].LineComment, m.Content[i+1].FootComment
		m.Content[i+1] = &node
		return nil
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &node)
	return nil
}

func mapDel(m *yaml.Node, key string) {
	if m == nil {
		return
	}
	if i := mapIndex(m, key); i >= 0 {
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
	}
}

// ensureMap returns the mapping stored at key, creating (or replacing a null
// placeholder with) an empty one.
func ensureMap(m *yaml.Node, key string) *yaml.Node {
	if existing := mapGet(m, key); existing != nil && existing.Kind == yaml.MappingNode {
		return existing
	}
	_ = mapSet(m, key, map[string]any{})
	return mapGet(m, key)
}

func nodeStrings(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, item := range n.Content {
		out = append(out, item.Value)
	}
	return out
}

func nodeStringMap(n *yaml.Node) map[string]string {
	out := map[string]string{}
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = n.Content[i+1].Value
	}
	return out
}
