package epp

import (
	"crypto/tls"
	"log"
	"net"
	"time"

	"github.com/google/uuid"
	xsd "github.com/lestrrat-go/libxml2/xsd"
)

// EPP 커맨드 처리 함수입니다.
type HandlerFunc func(*Session, []byte) ([]byte, error)

// EPP 커맨드 중 Greeting을 처리하는 함수입니다.
type GreetFunc func(*Session) ([]byte, error)

// 새롭게 생성되는 각 세션에 할당될 설정입니다.
type SessionConfig struct {
	// 연결이 끊기기 전에 서버에서 유휴 상태로 전환될 수 있는 최대 타임아웃 시간입니다.
	// 트래픽이 서버로 전송될 때마다 유휴 틱이 재설정 되고 새로운 IdleTimeout 시간이 할당됩니다.
	IdleTimeout time.Duration

	// 단일 세션에 허용되는 최대 지속 시간입니다.
	// 이 제한에 도달했을 때 클라이언트가 연결되어 있는 경우, 현재 명령어의 처리가 완료된 후 연결이 끊어집니다.
	SessionTimeout time.Duration

	// 클라이언트가 서버에 greeting으로 연결되어있을 때, XML을 생성하는 함수입니다.
	Greeting GreetFunc

	// 서버에 각 요청을 전달하는 핸들러 함수입니다.
	Handler HandlerFunc

	// validator 인터페이스를 구현하는 type 입니다.
	// validator 인터페이스는 XSD 스키마 또는 다른 방법으로 주어진 XML을 검증할 수 있어야만 합니다.
	// validator가 null이 아닌 경우, 들어오는 데이터 및 나가는 데이터들은 모두 validator를 통해 전달됩니다.
	// libxml2 바인딩을 사용하여 구현한 type 인터페이스를 라이브러리에서 사용할 수 있습니다.
	Validator Validator

	// 각 명령어를 통해 실행될 함수들입니다.
	// 각 명령어 뒤에 처리할 외부 코드를 넣는 곳입니다.
	OnCommands []func(sess *Session)
}

// EPP 서버에 대한 활성화된 연결입니다.
type Session struct {
	// 서버와 handshake를 하면서 시작된 TLS 연결의 상태를 나타냅니다.
	ConnectionState func() tls.ConnectionState

	// 특정 세션을 구별하기 위해 사용되는 고유한 ID입니다.
	SessionID string

	// 클라이언트와의 TCP 연결을 유지하는데 사용됩니다.
	conn net.Conn

	// 세션을 종료하도록 지시하는데 사용됩니다.
	stopChan chan struct{}

	// # SessionConfig 에서 사용되는 것들입니다.
	IdleTimeout    time.Duration
	SessionTimeout time.Duration
	greeting       GreetFunc
	handler        HandlerFunc
	onCommands     []func(sess *Session)
	validator      Validator
}

// 새로운 세션을 생성합니다.
func NewSession(conn *tls.Conn, cfg SessionConfig) *Session {
	sessionID := uuid.New().String()

	s := &Session{
		SessionID:       sessionID,
		ConnectionState: conn.ConnectionState,
		conn:            conn,
		stopChan:        make(chan struct{}),
		IdleTimeout:     cfg.IdleTimeout,
		SessionTimeout:  cfg.SessionTimeout,
		greeting:        cfg.Greeting,
		handler:         cfg.Handler,
		onCommands:      cfg.OnCommands,
		validator:       cfg.Validator,
	}

	return s
}

// 세션을 시작합니다.
func (s *Session) run() error {
	defer s.conn.Close()

	// greeting 프로세스를 처리하기 위해 클라이언트에게 보낼 greeting 을 생성합니다. (RFC5730 2.4)
	response, err := s.greeting(s)
	if err != nil {
		// TODO: Write response code and message?
		return err
	}

	// greeting을 보내기 전에, EPP XSD로 해당 메세지가 유효한 형식인지 확인합니다.
	if err := s.validate(response); err != nil {
		return err
	}

	// Socket에 greeting 을 작성하여 보냅니다.
	err = WriteMessage(s.conn, response)
	if err != nil {
		return err
	}

	// 세션과 유휴를 위해 타이머를 시작합니다.
	sessionTimeout := time.After(s.SessionTimeout)
	idleTimeout := time.After(s.IdleTimeout)

	for {
		select {
		case <-s.stopChan:
			log.Printf("stopping server, ending session %s", s.SessionID)

			return nil
		case <-sessionTimeout:
			log.Printf("session has been active for %v minutes, ending session %s", s.SessionTimeout.Minutes(), s.SessionID)

			return nil
		case <-idleTimeout:
			log.Printf("session has been idle for %v minutes, ending session %s", s.IdleTimeout.Minutes(), s.SessionID)

			return nil
		default:
			// 일부러 중단하는 것도 아니고 세션 또는 유휴가 타임아웃 난 것도 아니면 그냥 소켓에서 바로 읽습니다.
		}

		// 매초마다 읽기 전에 deadline을 설정하여 타임아웃을 확인하고 핸들링을 할 수 있도록 중단시킵니다.
		err = s.conn.SetDeadline(time.Now().Add(1 * time.Second))
		if err != nil {
			return err
		}

		// Socket을 읽었을 때 오류가 있다면 Socket에 아무런 활동이 없는한 무시합니다.
		message, err := ReadMessage(s.conn)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}

			return err
		}

		// 명령어를 실행하기 전에, 각 명령어에서 실행되도록 정의한 모든 함수를 실행합니다.
		// 사용자가 명령어를 실행시키기 전의 속도 제한 또는 기타 작업을 해야할 때 추가될 수 있습니다.
		for _, f := range s.onCommands {
			f(s)
		}

		// 전달받는 모든 XML 데이터는 RFC XSD로 전달하여 검증합니다.
		if err := s.validate(message); err != nil {
			return err
		}

		// 핸들러에 내용을 전달하여 작업을 수행하게 하거나 라우팅하게 만듭니다.
		response, err = s.handler(s, message)
		if err != nil {
			return err
		}

		// 핸들러에게서 받은 결과 내용을 RFC XSD로 전달하여 검증하고, 클라이언트에게 잘못된 XML을 보내지 않게 합니다.
		if err := s.validate(response); err != nil {
			return err
		}

		// Socket 에 내용을 작성합니다.
		err = WriteMessage(s.conn, response)
		if err != nil {
			return err
		}

		// 유휴 타임아웃을 연장합니다.
		idleTimeout = time.After(s.IdleTimeout)
	}
}

// 세션을 닫히게 합니다.
func (s *Session) Close() error {
	close(s.stopChan)

	if s.validator != nil {
		s.validator.Free()
	}

	return nil
}

// 전달받은 내용을 XSD에 전달하여 XML 형식을 검증합니다.
func (s *Session) validate(data []byte) error {
	if s.validator == nil {
		return nil
	}

	if err := s.validator.Validate(data); err != nil {
		if xErr, ok := err.(xsd.SchemaValidationError); ok {
			for _, e := range xErr.Errors() {
				log.Printf("error: %s", e.Error())
			}
		}

		return err
	}

	return nil
}
