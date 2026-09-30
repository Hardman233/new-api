package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type xaiFileFilterTokenKind int

const (
	xaiFilterEOF xaiFileFilterTokenKind = iota
	xaiFilterIdentifier
	xaiFilterString
	xaiFilterNumber
	xaiFilterNull
	xaiFilterOperator
	xaiFilterAnd
	xaiFilterOr
	xaiFilterNot
	xaiFilterLeftParen
	xaiFilterRightParen
)

type xaiFileFilterToken struct {
	kind xaiFileFilterTokenKind
	text string
}

type xaiFileFilterNode struct {
	sql  string
	args []any
}

type xaiFileFilterParser struct {
	tokens []xaiFileFilterToken
	index  int
}

func parseXAIFileFilter(input string) (string, []any, error) {
	tokens, err := tokenizeXAIFileFilter(input)
	if err != nil {
		return "", nil, err
	}
	parser := &xaiFileFilterParser{tokens: tokens}
	node, err := parser.parseOr()
	if err != nil {
		return "", nil, err
	}
	if parser.peek().kind != xaiFilterEOF {
		return "", nil, fmt.Errorf("unexpected token %q", parser.peek().text)
	}
	return node.sql, node.args, nil
}

func tokenizeXAIFileFilter(input string) ([]xaiFileFilterToken, error) {
	var tokens []xaiFileFilterToken
	for index := 0; index < len(input); {
		current := rune(input[index])
		if unicode.IsSpace(current) {
			index++
			continue
		}
		switch input[index] {
		case '(':
			tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterLeftParen, text: "("})
			index++
			continue
		case ')':
			tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterRightParen, text: ")"})
			index++
			continue
		case '=', '!', '>', '<':
			start := index
			index++
			if index < len(input) && input[index] == '=' {
				index++
			}
			operator := input[start:index]
			if operator == "!" {
				return nil, fmt.Errorf("expected !=")
			}
			tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterOperator, text: operator})
			continue
		case '\'', '"':
			quote := input[index]
			index++
			var value strings.Builder
			closed := false
			for index < len(input) {
				if input[index] == '\\' && index+1 < len(input) {
					index++
					value.WriteByte(input[index])
					index++
					continue
				}
				if input[index] == quote {
					if index+1 < len(input) && input[index+1] == quote {
						value.WriteByte(quote)
						index += 2
						continue
					}
					index++
					closed = true
					break
				}
				value.WriteByte(input[index])
				index++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string literal")
			}
			tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterString, text: value.String()})
			continue
		}

		if isXAIFileFilterIdentifierStart(input[index]) {
			start := index
			for index < len(input) && isXAIFileFilterIdentifierPart(input[index]) {
				index++
			}
			word := input[start:index]
			switch strings.ToUpper(word) {
			case "AND":
				tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterAnd, text: word})
			case "OR":
				tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterOr, text: word})
			case "NOT":
				tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterNot, text: word})
			case "NULL":
				tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterNull, text: word})
			default:
				tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterIdentifier, text: word})
			}
			continue
		}
		if input[index] == '-' || (input[index] >= '0' && input[index] <= '9') {
			start := index
			index++
			for index < len(input) && input[index] >= '0' && input[index] <= '9' {
				index++
			}
			tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterNumber, text: input[start:index]})
			continue
		}
		return nil, fmt.Errorf("unexpected character %q", input[index])
	}
	tokens = append(tokens, xaiFileFilterToken{kind: xaiFilterEOF})
	return tokens, nil
}

func isXAIFileFilterIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isXAIFileFilterIdentifierPart(value byte) bool {
	return isXAIFileFilterIdentifierStart(value) || value >= '0' && value <= '9'
}

func (p *xaiFileFilterParser) parseOr() (xaiFileFilterNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return xaiFileFilterNode{}, err
	}
	for p.peek().kind == xaiFilterOr {
		p.next()
		right, parseErr := p.parseAnd()
		if parseErr != nil {
			return xaiFileFilterNode{}, parseErr
		}
		left = combineXAIFileFilterNodes(left, "OR", right)
	}
	return left, nil
}

func (p *xaiFileFilterParser) parseAnd() (xaiFileFilterNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return xaiFileFilterNode{}, err
	}
	for p.peek().kind == xaiFilterAnd {
		p.next()
		right, parseErr := p.parseUnary()
		if parseErr != nil {
			return xaiFileFilterNode{}, parseErr
		}
		left = combineXAIFileFilterNodes(left, "AND", right)
	}
	return left, nil
}

func (p *xaiFileFilterParser) parseUnary() (xaiFileFilterNode, error) {
	if p.peek().kind == xaiFilterNot {
		p.next()
		node, err := p.parseUnary()
		if err != nil {
			return xaiFileFilterNode{}, err
		}
		return xaiFileFilterNode{sql: "NOT (" + node.sql + ")", args: node.args}, nil
	}
	if p.peek().kind == xaiFilterLeftParen {
		p.next()
		node, err := p.parseOr()
		if err != nil {
			return xaiFileFilterNode{}, err
		}
		if p.peek().kind != xaiFilterRightParen {
			return xaiFileFilterNode{}, fmt.Errorf("expected closing parenthesis")
		}
		p.next()
		return node, nil
	}
	return p.parseComparison()
}

func (p *xaiFileFilterParser) parseComparison() (xaiFileFilterNode, error) {
	field := p.next()
	if field.kind != xaiFilterIdentifier {
		return xaiFileFilterNode{}, fmt.Errorf("expected filter field")
	}
	operator := p.next()
	if operator.kind != xaiFilterOperator || !validXAIFileFilterOperator(operator.text) {
		return xaiFileFilterNode{}, fmt.Errorf("expected comparison operator after %s", field.text)
	}
	value := p.next()
	if value.kind != xaiFilterString && value.kind != xaiFilterNumber && value.kind != xaiFilterNull {
		return xaiFileFilterNode{}, fmt.Errorf("expected comparison value after %s", operator.text)
	}
	return buildXAIFileFilterComparison(field.text, operator.text, value)
}

func (p *xaiFileFilterParser) peek() xaiFileFilterToken {
	if p.index >= len(p.tokens) {
		return xaiFileFilterToken{kind: xaiFilterEOF}
	}
	return p.tokens[p.index]
}

func (p *xaiFileFilterParser) next() xaiFileFilterToken {
	token := p.peek()
	if p.index < len(p.tokens) {
		p.index++
	}
	return token
}

func validXAIFileFilterOperator(operator string) bool {
	switch operator {
	case "=", "!=", ">", ">=", "<", "<=":
		return true
	default:
		return false
	}
}

func combineXAIFileFilterNodes(left xaiFileFilterNode, operator string, right xaiFileFilterNode) xaiFileFilterNode {
	args := append(append(make([]any, 0, len(left.args)+len(right.args)), left.args...), right.args...)
	return xaiFileFilterNode{sql: "(" + left.sql + ") " + operator + " (" + right.sql + ")", args: args}
}

func buildXAIFileFilterComparison(field, operator string, value xaiFileFilterToken) (xaiFileFilterNode, error) {
	field = strings.ToLower(field)
	columns := map[string]string{
		"file_id":               "file_id",
		"id":                    "file_id",
		"name":                  "filename",
		"file_name":             "filename",
		"filename":              "filename",
		"size":                  "size_bytes",
		"size_bytes":            "size_bytes",
		"content_type":          "content_type",
		"created_at":            "created_at",
		"expires_at":            "expires_at",
		"public_url":            "public_url",
		"public_url_expires_at": "public_url_expires_at",
	}
	if field == "upload_status" {
		return buildXAIFileStaticComparison("completed", operator, value)
	}
	if field == "user_defined_id" {
		return buildXAIFileStaticNullComparison(operator, value)
	}
	column, ok := columns[field]
	if !ok {
		return xaiFileFilterNode{}, fmt.Errorf("unsupported filter field %q", field)
	}
	if value.kind == xaiFilterNull {
		return buildXAIFileNullComparison(field, column, operator)
	}

	var argument any
	switch field {
	case "size", "size_bytes":
		if value.kind != xaiFilterNumber {
			return xaiFileFilterNode{}, fmt.Errorf("%s requires an integer value", field)
		}
		parsed, err := strconv.ParseInt(value.text, 10, 64)
		if err != nil {
			return xaiFileFilterNode{}, fmt.Errorf("invalid integer for %s", field)
		}
		argument = parsed
	case "created_at", "expires_at", "public_url_expires_at":
		if value.kind == xaiFilterNumber {
			parsed, err := strconv.ParseInt(value.text, 10, 64)
			if err != nil {
				return xaiFileFilterNode{}, fmt.Errorf("invalid timestamp for %s", field)
			}
			argument = parsed
		} else {
			parsed, err := time.Parse(time.RFC3339, value.text)
			if err != nil {
				return xaiFileFilterNode{}, fmt.Errorf("%s requires an RFC3339 timestamp", field)
			}
			argument = parsed.Unix()
		}
	default:
		if value.kind != xaiFilterString {
			return xaiFileFilterNode{}, fmt.Errorf("%s requires a string value", field)
		}
		argument = value.text
	}
	if operator == "!=" {
		operator = "<>"
	}
	return xaiFileFilterNode{sql: column + " " + operator + " ?", args: []any{argument}}, nil
}

func buildXAIFileNullComparison(field, column, operator string) (xaiFileFilterNode, error) {
	if operator != "=" && operator != "!=" {
		return xaiFileFilterNode{}, fmt.Errorf("null only supports = and !=")
	}
	nullSQL := "1 = 0"
	switch field {
	case "expires_at", "public_url_expires_at":
		nullSQL = column + " = 0"
	case "public_url":
		nullSQL = "(" + column + " = '' OR " + column + " IS NULL)"
	}
	if operator == "!=" {
		nullSQL = "NOT (" + nullSQL + ")"
	}
	return xaiFileFilterNode{sql: nullSQL}, nil
}

func buildXAIFileStaticComparison(actual string, operator string, value xaiFileFilterToken) (xaiFileFilterNode, error) {
	if value.kind != xaiFilterString {
		return xaiFileFilterNode{}, fmt.Errorf("upload_status requires a string value")
	}
	comparison := strings.Compare(actual, value.text)
	matched := compareXAIFileFilterOrder(comparison, operator)
	return xaiFileBooleanNode(matched), nil
}

func buildXAIFileStaticNullComparison(operator string, value xaiFileFilterToken) (xaiFileFilterNode, error) {
	if operator != "=" && operator != "!=" {
		return xaiFileFilterNode{}, fmt.Errorf("user_defined_id only supports = and !=")
	}
	isNull := value.kind == xaiFilterNull
	if value.kind != xaiFilterNull && value.kind != xaiFilterString {
		return xaiFileFilterNode{}, fmt.Errorf("user_defined_id requires a string or null")
	}
	matched := isNull
	if operator == "!=" {
		matched = !matched
	}
	return xaiFileBooleanNode(matched), nil
}

func compareXAIFileFilterOrder(comparison int, operator string) bool {
	switch operator {
	case "=":
		return comparison == 0
	case "!=":
		return comparison != 0
	case ">":
		return comparison > 0
	case ">=":
		return comparison >= 0
	case "<":
		return comparison < 0
	case "<=":
		return comparison <= 0
	default:
		return false
	}
}

func xaiFileBooleanNode(value bool) xaiFileFilterNode {
	if value {
		return xaiFileFilterNode{sql: "1 = 1"}
	}
	return xaiFileFilterNode{sql: "1 = 0"}
}
