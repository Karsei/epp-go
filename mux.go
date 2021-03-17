package epp

import (
	"strings"

	"aqwari.net/xml/xmltree"
	"github.com/bombsimon/epp-go/types"
	"github.com/pkg/errors"
)

const nsEPP = "urn:ietf:params:xml:ns:epp-1.0"

// Mux 는 메시지 내용에 따라 서로 다른 핸들러에게 서로 다른 EPP 메시지를 라우트하도록 사용됩니다.
//
//  m := Mux{}
//
//  m.AddNamespaceAlias("urn:ietf:params:xml:ns:domain-1.0", "domain")
//
//  m.AddHandler("hello", handleHello)
//  m.AddHandler("command/login", handleLogin)
//  m.AddHandler("command/check/urn:ietf:params:xml:ns:contact-1.0", handleCheckContact)
//  m.AddHandler("command/check/domain", handleCheckDomain)
type Mux struct {
	handlers         map[string]HandlerFunc
	namespaceAliases map[string]string
}

// 새로운 Mux를 생성하고 반환합니다.
func NewMux() *Mux {
	m := &Mux{
		namespaceAliases: map[string]string{
			types.NameSpaceDomain:  "domain",
			types.NameSpaceHost:    "host",
			types.NameSpaceContact: "contact",
		},
		handlers: make(map[string]HandlerFunc),
	}

	return m
}

// 지정된 네임스페이스에 별칭을 추가합니다. 별칭을 추가하면 라우팅에서 사용될 수 있습니다.
// 여러 개의 네임스페이스는 같은 별칭으로 추가될 수 있습니다.
//  m.AddNamespaceAlias("urn:ietf:params:xml:ns:contact-1.0", "host-and-contact")
//  m.AddNamespaceAlias("urn:ietf:params:xml:ns:host-1.0", "host-and-contact")
func (m *Mux) AddNamespaceAlias(ns, alias string) {
	m.namespaceAliases[ns] = alias
}

// 지정된 라우트에 대해 핸들러를 등록합니다.
// 라우트는 xpath 처럼 정의됩니다.
func (m *Mux) AddHandler(path string, handler HandlerFunc) {
	m.handlers[path] = handler
}

// 들어오는 메시지를 가지고서 알맞는 핸들러로 라우트합니다.
// Mux를 사용하는 Server로 함수를 전달해야 합니다.
func (m *Mux) Handle(s *Session, d []byte) ([]byte, error) {
	root, err := xmltree.Parse(d)
	if err != nil {
		return nil, err
	}

	path, err := m.buildPath(root)
	if err != nil {
		return nil, err
	}

	h, ok := m.handlers[path]
	if !ok {
		// TODO
		return nil, errors.Errorf("no handler for %s", path)
	}

	return h(s, d)
}

func (m *Mux) buildPath(root *xmltree.Element) (string, error) {
	// 첫 번째 요소가 <epp>로 시작하는지 확인합니다.
	if root.Name.Space != nsEPP || root.Name.Local != "epp" {
		return "", errors.New("missing <epp> tag")
	}

	// <epp> 태그 안에는 오직 하나의 요소만 있어야 합니다.
	if len(root.Children) != 1 {
		return "", errors.New("<epp> should contain one element")
	}

	el := root.Children[0]
	if el.Name.Local != "command" {
		// 명령어가 아니라면, 이 태그를 라우트 용도로 사용되도록 합니다.
		return el.Name.Local, nil
	}

	pathParts := []string{"command"}
	for _, child := range el.Children {
		name := child.Name.Local

		switch name {
		case "extension", "clTRID":
			// 이러한 태그들은 명령어에 존재할 수 있지만 항상 이용할 수 있으므로
			// 이것들을 기반으로 라우팅을 하지 않습니다.
			continue
		}

		switch name {
		case "login", "logout", "poll":
			// Login, Logout, Poll 은 eppcom-1.0.xml 에 정의된 명령어들입니다.
			// 그 어떤 라우팅도 하지 않습니다.
			pathParts = append(pathParts, name)
		default:
			// 다른 명령어들은 네임스페이스로 지정된 여러 유형의 개체들에서 실행될 수 있습니다.
			ns := child.Children[0].Name.Space

			if alias, ok := m.namespaceAliases[ns]; ok {
				ns = alias
			}

			pathParts = append(pathParts, name, ns)
		}

		break
	}

	return strings.Join(pathParts, "/"), nil
}
