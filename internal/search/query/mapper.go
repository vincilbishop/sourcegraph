package query

import "fmt"

// Mapper replaces visited nodes by the returned value.
type Mapper struct {
	MapOperator  func(kind operatorKind, operands []Node) Node
	MapParameter func(field, value string, negated bool, annotation Annotation) Node
	MapPattern   func(value string, negated bool, annotation Annotation) Node
}

func (m *Mapper) Map(n Node) Node {
	switch n := n.(type) {
	case Operator:
		operands := make([]Node, 0, len(n.Operands))
		for _, o := range n.Operands {
			operands = append(operands, m.Map(o))
		}
		if m.MapOperator != nil {
			return m.MapOperator(n.Kind, operands)
		}
		return Operator{Kind: n.Kind, Operands: operands}
	case Parameter:
		if m.MapParameter != nil {
			return m.MapParameter(n.Field, n.Value, n.Negated, n.Annotation)
		}
		return n
	case Pattern:
		if m.MapPattern != nil {
			return m.MapPattern(n.Value, n.Negated, n.Annotation)
		}
		return n
	default:
		panic(fmt.Sprintf("unsupported node type %T for query.Mapper", n))
	}
}

func (m *Mapper) MapN(nodes []Node) []Node {
	newNodes := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		newNodes = append(newNodes, m.Map(n))
	}
	return NewOperator(newNodes, And)
}

// MapOperator is a convenience function that calls the mapping function `f` on
// all operator nodes, reducing the modified tree if needed.
func MapOperator(nodes []Node, f func(kind operatorKind, operands []Node) Node) []Node {
	mapper := &Mapper{MapOperator: f}
	return mapper.MapN(nodes)
}

// MapParameter is a convenience function that calls the mapping function `f` on all parameters.
func MapParameter(nodes []Node, f func(field, value string, negated bool, annotation Annotation) Node) []Node {
	mapper := &Mapper{MapParameter: f}
	return mapper.MapN(nodes)
}

// MapPattern is a convenience function that calls the mapping function `f` on all patterns.
func MapPattern(nodes []Node, f func(value string, negated bool, annotation Annotation) Node) []Node {
	mapper := &Mapper{MapPattern: f}
	return mapper.MapN(nodes)
}

// MapField is a convenience function that calls the mapping function `f` on all
// parameter nodes whose field name matches the `field` argument.
func MapField(nodes []Node, field string, f func(value string, negated bool, annotation Annotation) Node) []Node {
	return MapParameter(nodes, func(gotField, value string, negated bool, annotation Annotation) Node {
		if field == gotField {
			return f(value, negated, annotation)
		}
		return Parameter{Field: gotField, Value: value, Negated: negated, Annotation: annotation}
	})
}
