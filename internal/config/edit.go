package config

import (
	"fmt"
	"go.yaml.in/yaml/v3"
)

// ReplaceRequest preserves unrelated YAML nodes and comments. Only the selected
// recipe is replaced, and the complete result is validated before it is returned.
func ReplaceRequest(source []byte, id string, request Request) ([]byte, error) {
	if _, e := Parse(source); e != nil {
		return nil, e
	}
	var document yaml.Node
	if e := yaml.Unmarshal(source, &document); e != nil {
		return nil, e
	}
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "requests" {
			continue
		}
		sequence := root.Content[i+1]
		for _, node := range sequence.Content {
			for j := 0; j+1 < len(node.Content); j += 2 {
				if node.Content[j].Value == "id" && node.Content[j+1].Value == id {
					var replacement yaml.Node
					if e := replacement.Encode(request); e != nil {
						return nil, e
					}
					replacement.HeadComment = node.HeadComment
					replacement.FootComment = node.FootComment
					*node = replacement
					b, e := yaml.Marshal(&document)
					if e != nil {
						return nil, e
					}
					if _, e = Parse(b); e != nil {
						return nil, e
					}
					return b, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("request %q not found", id)
}
