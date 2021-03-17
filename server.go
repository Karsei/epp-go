package epp

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// 요청을 처리하는 서버를 나타냅니다.
type Server struct {
	// TCP 연결에서 수신할 주소입니다.
	// 모든 인터페이스에서 기본 EPP 포트 700으로 수신 트래픽에 접근하려면 ':700' 과 같은 값으로 설정해야 합니다.
	Addr string

	// 서버가 시작되고 나서 실행하게 될 함수들입니다.
	OnStarteds []func()

	// 각 세션이 만들어질 때 사용하기 위한 설정입니다.
	SessionConfig SessionConfig

	// 인증서나 클라이언트 인증 등과 같은 설정을 가진 TLS 설정입니다.
	TLSConfig *tls.Config

	// 현재 활성화되어 있는 모든 세션입니다.
	Sessions map[string]*Session

	// 세션 목록의 읽기, 쓰기 작업에 Thread Safe 접근을 보장하기 위한 Mutex 입니다.
	sessionsMu sync.Mutex

	// 서버가 종료되기 전에 현재 모든 진행중인 세션들이 반드시 완료되는 것을 보장하기 위해 사용되는 WaitGroup 입니다.
	sessionsWg sync.WaitGroup

	// 서버가 자연스럽게 종료를 해야할 때 닫힐 것임을 알려주기 위한 채널입니다.
	stopChan chan struct{}
}

// EPP 서버를 시작합니다.
func (s *Server) ListenAndServe() error {
	addr, err := net.ResolveTCPAddr("tcp", s.Addr)
	if err != nil {
		return err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return err
	}

	err = s.Serve(l)
	if err != nil {
		return err
	}

	return nil
}

// TCP 리스너를 통해 연결을 설정합니다.
func (s *Server) Serve(l *net.TCPListener) error {
	s.sessionsWg = sync.WaitGroup{}
	s.stopChan = make(chan struct{})
	s.Sessions = map[string]*Session{}

	// 서버가 종료될 경우 수행됨
	defer func() {
		if closeErr := l.Close(); closeErr != nil {
			fmt.Println(closeErr.Error())
		}

		s.sessionsWg.Wait()
	}()

	tlsConfig := &tls.Config{}

	// 기존에 사용하던 설정이면 세션에 같은 TLS 설정을 부여합니다.
	if s.TLSConfig != nil {
		tlsConfig = s.TLSConfig.Clone()
	}

	// 서버가 실행될 때마다 수행할 사용자 정의 함수를 실행합니다.
	for _, f := range s.OnStarteds {
		f()
	}

	for {
		// 연결을 허용할 때 blocking을 막고 shutdown을 허용하기 위해 TCP리스너의 deadline을 초기화합니다.
		if err := l.SetDeadline(time.Now().Add(1 * time.Second)); err != nil {
			return err
		}

		conn, err := l.AcceptTCP()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				select {
				case <-s.stopChan:
					return nil
				default:
					continue
				}
			}

			return err
		}

		if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
			log.Printf(fmt.Sprintf("client connected: %s on %s/%d", addr.IP.String(), addr.Network(), addr.Port))
		}

		// 아무 활동없이 반드시 최대 10분까지 연결을 허용해야 TCP 소켓에서 keepalive를 활성화할 수 있습니다.
		if err := conn.SetKeepAlive(true); err != nil {
			log.Println(err.Error())
			continue
		}

		if err := conn.SetKeepAlivePeriod(1 * time.Minute); err != nil {
			log.Printf(err.Error())
			continue
		}

		go s.startSession(conn, tlsConfig)

		log.Printf("start sessions..")
	}
}

func (s *Server) startSession(conn net.Conn, tlsConfig *tls.Config) {
	// TLS를 초기화합니다.
	tlsConn := tls.Server(conn, tlsConfig)

	err := tlsConn.Handshake()
	if err != nil {
		log.Println(err.Error())

		return
	}

	session := NewSession(tlsConn, s.SessionConfig)

	// 인덱스에 세션을 확실하게 추가되도록 합니다.
	s.sessionsWg.Add(1)
	s.sessionsMu.Lock()
	s.Sessions[session.SessionID] = session
	s.sessionsMu.Unlock()

	// 세션이 종료되고 나서 세션 인덱스에 있는 해당 세션을 확실하게 제거되도록 합니다.
	defer func() {
		s.sessionsMu.Lock()

		if _, ok := s.Sessions[session.SessionID]; ok {
			delete(s.Sessions, session.SessionID)
		}

		s.sessionsMu.Unlock()
		s.sessionsWg.Done()

		log.Println("session completed")
	}()

	log.Println("starting session", session.SessionID)

	if err = session.run(); err != nil {
		log.Println(err)
	}
}

// 요청이 처리되지 않도록 채널을 닫고 현재 진행중인 모든 요청을 중단합니다.
func (s *Server) Stop() {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	log.Print("stopping listener channel")

	close(s.stopChan)

	for _, session := range s.Sessions {
		if err := session.Close(); err != nil {
			log.Println("error closing session:", err.Error())
		}
	}
}
