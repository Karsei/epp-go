package epp

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"time"

	"aqwari.net/xml/xmltree"
	"github.com/bombsimon/epp-go/types"
)

const (
	rootLocalName = "epp"
)

var (
	connectionError   = errors.New("connection error")
	contentIsTooLarge = errors.New("content is too large")
)

// 하나의 전체 메시지를 읽습니다.
func ReadMessage(conn net.Conn) ([]byte, error) {
	if conn == nil {
		return nil, connectionError
	}

	// https://tools.ietf.org/html/rfc5734#section-4
	var totalSize uint32

	if err := binary.Read(conn, binary.BigEndian, &totalSize); err != nil {
		return nil, err
	}

	headerSize := binary.Size(totalSize)
	contentSize := int(totalSize) - headerSize

	// 메시지를 읽을 때 충분한 시간이 반드시 보장되도록 합니다.
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}

	buf := make([]byte, contentSize)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}

	return buf, nil
}

// 적절한 헤더를 가지고 데이터를 작성합니다.
func WriteMessage(conn net.Conn, data []byte) error {
	// len(b)를 Big Endian uint32로 작성하면 헤더의 콘텐츠 길이 사이즈를 포함하여 시작됩니다.
	// https://tools.ietf.org/html/rfc5734#section-4
	contentSize := len(data)
	headerSize := binary.Size(uint32(contentSize))
	totalSize := contentSize + headerSize

	// 범위 체크
	if totalSize > math.MaxUint32 {
		return contentIsTooLarge
	}

	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}

	if err := binary.Write(conn, binary.BigEndian, uint32(totalSize)); err != nil {
		return err
	}

	if _, err := conn.Write(data); err != nil {
		return err
	}

	return nil
}

// 서버 응답으로부터 기본 속성을 정의합니다.
func ServerXMLAttributes() []xml.Attr {
	return []xml.Attr{
		{
			Name: xml.Name{
				Space: "",
				Local: "xmlns",
			},
			Value: "urn:ietf:params:xml:ns:epp-1.0",
		},
		{
			Name: xml.Name{
				Space: "",
				Local: "xmlns:xsi",
			},
			Value: "http://www.w3.org/2001/XMLSchema-instance",
		},
		{
			Name: xml.Name{
				Space: "",
				Local: "xsi:schemaLocation",
			},
			Value: "urn:ietf:params:xml:ns:epp-1.0 epp-1.0.xsd",
		},
	}
}

// 클라이언트 요청에 있는 기본 속성을 정의합니다.
func ClientXMLAttributes() []xml.Attr {
	return []xml.Attr{
		{
			Name: xml.Name{
				Space: "",
				Local: "xmlns",
			},
			Value: types.NameSpaceEPP10,
		},
	}
}

// XML을 Marshal 할 수 있는 type을 가지고
// 등록된 모든 네임스페이스 중에서 매치되는 EPP 태그를 붙여 Byte 조각으로 XML을 반환합니다.
func Encode(data interface{}, xmlAttributes []xml.Attr) ([]byte, error) {
	// Input 데이터를 Marshal 하여 XML로 뽑아내고, 요구되는 태그 및 기능으로 type을 유추합니다.
	b, err := xml.Marshal(data)
	if err != nil {
		return nil, err
	}

	document, err := xmltree.Parse(b)
	if err != nil {
		return nil, err
	}

	addNameSpaceAlias(document, false)

	// document root 요소를 적절한 EPP 태그로 변경합니다.
	document.StartElement = xml.StartElement{
		Name: xml.Name{
			Space: "",
			Local: rootLocalName,
		},
		Attr: xmlAttributes,
	}

	// 네임스페이스와 속성을 고친 후 xmltree를 Marshal 합니다.
	xmlBytes := xmltree.MarshalIndent(document, "", "  ")

	// Marshal 되어있는 문서에 XML Header를 붙입니다.
	xmlBytes = append([]byte(xml.Header), xmlBytes...)

	return xmlBytes, nil
}

// XML 구조 안에 있는 각 노드/요소를 체크하여 만약 xml.Name.Space를 가지고 있을 경우
// 별칭이 생성되고 모든 자식 노드들에 덧붙입니다.
// 별칭은 root 요소에 대해서만 설정됩니다.
func addNameSpaceAlias(document *xmltree.Element, nsAdded bool) *xmltree.Element {
	namespaceAliases := map[string]string{
		types.NameSpaceDomain:   "domain",
		types.NameSpaceHost:     "host",
		types.NameSpaceContact:  "contact",
		types.NameSpaceDNSSEC10: "sed",
		types.NameSpaceDNSSEC11: "sec",
		types.NameSpaceIIS12:    "iis",
	}

	if document.Name.Space != "" {
		alias, ok := namespaceAliases[document.Name.Space]
		if !ok {
			return nil
		}

		if !nsAdded {
			xmlns := fmt.Sprintf("xmlns:%s", alias)
			document.SetAttr("", xmlns, document.Name.Space)

			// 네임스페이스 별칭이 추가되었으므로 자식 요소들을 건너뛰기 위해 true로 변경합니다.
			nsAdded = true
		}

		document.Name.Local = fmt.Sprintf("%s:%s", alias, document.Name.Local)
	}

	for i, child := range document.Children {
		document.Children[i] = *addNameSpaceAlias(&child, nsAdded)
	}

	return document
}
