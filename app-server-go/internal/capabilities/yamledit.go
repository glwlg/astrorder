package capabilities

import (
	"bytes"
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// Hermes settings are user-editable. Modify the syntax tree so comments,
// untouched values, anchors and key order survive configuration updates.
func yamlParse(data []byte) (*ast.File, *ast.MappingNode, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}\n")
	}
	doc, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	if len(doc.Docs) != 1 {
		return nil, nil, fmt.Errorf("config.yaml 必须只包含一个文档")
	}
	root, ok := doc.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return nil, nil, fmt.Errorf("config.yaml 顶层不是映射")
	}
	return doc, root, nil
}

func yamlDump(doc *ast.File) ([]byte, error) {
	data := []byte(doc.String())
	// Validate the edited tree before the caller writes it to disk.
	if _, err := parseYAMLMap(data); err != nil {
		return nil, err
	}
	return data, nil
}

func mapIndex(m *ast.MappingNode, key string) int {
	for i, entry := range m.Values {
		if scalarText(entry.Key) == key {
			return i
		}
	}
	return -1
}

func mapGet(m *ast.MappingNode, key string) ast.Node {
	if m == nil {
		return nil
	}
	if i := mapIndex(m, key); i >= 0 {
		return m.Values[i].Value
	}
	return nil
}

func mapSet(m *ast.MappingNode, key string, value any) error {
	data, err := yaml.MarshalWithOptions(yaml.MapSlice{{Key: key, Value: value}}, yaml.Indent(2), yaml.IndentSequence(true))
	if err != nil {
		return err
	}
	_, generated, err := yamlParse(data)
	if err != nil {
		return err
	}
	entry := generated.Values[0]
	entry.SetIsFlowStyle(m.IsFlowStyle)
	column := m.GetToken().Position.Column
	if len(m.Values) > 0 {
		column = m.Values[0].Key.GetToken().Position.Column
	}
	entry.AddColumn(column - entry.Key.GetToken().Position.Column)
	if i := mapIndex(m, key); i >= 0 {
		old := m.Values[i]
		entry.Key = old.Key
		entry.Comment, entry.FootComment = old.Comment, old.FootComment
		if err := entry.Value.SetComment(old.Value.GetComment()); err != nil {
			return err
		}
		m.Values[i] = entry
	} else {
		m.Values = append(m.Values, entry)
	}
	return nil
}

func mapDel(node ast.Node, key string) {
	m, ok := node.(*ast.MappingNode)
	if !ok || m == nil {
		return
	}
	if i := mapIndex(m, key); i >= 0 {
		m.Values = append(m.Values[:i], m.Values[i+1:]...)
	}
}

// ensureMap creates a missing mapping or replaces a null placeholder.
func ensureMap(m *ast.MappingNode, key string) *ast.MappingNode {
	if existing, ok := mapGet(m, key).(*ast.MappingNode); ok {
		return existing
	}
	_ = mapSet(m, key, map[string]any{})
	created, _ := mapGet(m, key).(*ast.MappingNode)
	return created
}

func scalarText(node ast.Node) string {
	if scalar, ok := node.(ast.ScalarNode); ok {
		return asString(scalar.GetValue())
	}
	return ""
}

func nodeStrings(node ast.Node) []string {
	n, ok := node.(*ast.SequenceNode)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(n.Values))
	for _, item := range n.Values {
		out = append(out, scalarText(item))
	}
	return out
}

func nodeStringMap(node ast.Node) map[string]string {
	out := map[string]string{}
	n, ok := node.(*ast.MappingNode)
	if !ok {
		return out
	}
	for _, entry := range n.Values {
		out[scalarText(entry.Key)] = scalarText(entry.Value)
	}
	return out
}
