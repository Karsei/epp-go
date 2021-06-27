package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pkg/errors"

	epp "github.com/bombsimon/epp-go"
	"github.com/bombsimon/epp-go/types"

	"database/sql"

	"gopkg.in/yaml.v2"

	_ "github.com/godror/godror"
)

func main() {
	// Mux 초기화
	mux := epp.NewMux()

	// Config 로드
	filename, _ := filepath.Abs("../../config.yml")
	yamlFile, err := ioutil.ReadFile(filename)
	var config epp.Config
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		panic(err)
	}
	var dbConfig epp.DatabaseEnv

	// Flag
	envPtr := flag.String("env", "production", "application environment. [production|development]")
	flag.Parse()
	if *envPtr == "development" {
		dbConfig = config.Database.Development
		log.Println(fmt.Sprintf("# This will be runned in the development environment. Debug statements may appear on this console."))
	} else {
		dbConfig = config.Database.Production
	}

	// 로드 시간 초기화
	startTime := time.Now()

	// XSD Validator 초기화
	validator, err := epp.NewValidator("../../xml/index.xsd")
	if err != nil {
		panic(err)
	}

	// MySQL 연결 초기화
	log.Println(fmt.Sprintf("Initializing Oracle Database..."))
	db, err := sql.Open("godror", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", "root", dbConfig.Oracle.User, dbConfig.Oracle.Host, dbConfig.Oracle.Port, dbConfig.Oracle.Database))
	if err != nil {
		log.Fatal(err)
		panic(err)
	}
	defer db.Close()

	// 인증서 조회
	cert, err := tls.LoadX509KeyPair("../../cert/server.crt", "../../cert/server.key")
	if err != nil {
		panic(err)
	}
	//cert := generateCertificate()

	server := epp.Server{
		// 포트
		Addr: fmt.Sprintf(":%d", config.Server.Port),
		// TLS 설정
		TLSConfig: &tls.Config{
			// 인증서
			Certificates: []tls.Certificate{cert},
			// 클라이언트 인증 타입 (https://golang.org/src/crypto/tls/common.go?s=9726:9749#L283)
			ClientAuth: tls.RequireAnyClientCert, // tls.RequireAnyClientCert,
		},
		// 세션 설정
		SessionConfig: epp.SessionConfig{
			// 유휴 제한시간
			IdleTimeout: 5 * time.Minute,
			// 세션 제한시간
			SessionTimeout: 10 * time.Minute,
			// Greeting
			Greeting: greeting,
			// 커맨드 핸들러
			Handler: mux.Handle,
			// 명령어를 전달받았을 때 실행될 콜백
			OnCommands: []func(sess *epp.Session){
				func(sess *epp.Session) {
					log.Printf("this command was brought to you by %s", sess.SessionID)
				},
			},
			Validator: validator,
		},
		OnStarteds: []func() {
			func() {
				estTime := time.Since(startTime)
				log.Printf("Done! Estimated Time: %s", estTime)
			},
		},
	}

	// 명령어에 대한 핸들러 등록
	mux.AddHandler("command/login", login)
	mux.AddHandler("command/info/domain", infoDomainWithExtension)
	mux.AddHandler("command/create/domain", createDomain)
	mux.AddHandler("command/create/contact", createContactWithExtension)

	// Graceful 서버 종료 지원
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		<-sigs
		server.Stop()
	}()

	log.Println(fmt.Sprintf("Listening server on %s...", server.Addr))

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err.Error())
	}
}

func greeting(s *epp.Session) ([]byte, error) {
	err := verifyClientCertificate(s.ConnectionState().PeerCertificates)
	if err != nil {
		_ = s.Close()

		fmt.Println("could not verify peer certificates")

		return nil, errors.New("could not verify certificates")
	}

	greeting := types.EPPGreeting{
		Greeting: types.Greeting{
			ServerID:   "default-server",
			ServerDate: time.Now(),
			ServiceMenu: types.ServiceMenu{
				Version:  []string{"1.0"},
				Language: []string{"en"},
				ObjectURI: []string{
					types.NameSpaceDomain,
					types.NameSpaceContact,
					types.NameSpaceHost,
				},
			},
			DCP: types.DCP{
				Access: types.DCPAccess{
					All: &types.EmptyTag{},
				},
				Statement: types.DCPStatement{
					Purpose: types.DCPPurpose{
						Prov: types.Empty(),
					},
					Recipient: types.DCPRecipient{
						Ours:   []types.DCPOurs{{}},
						Public: types.Empty(),
					},
					Retention: types.DCPRetention{
						Stated: types.Empty(),
					},
				},
			},
		},
	}

	return epp.Encode(greeting, epp.ServerXMLAttributes())
}

func login(s *epp.Session, data []byte) ([]byte, error) {
	login := types.Login{}

	if err := xml.Unmarshal(data, &login); err != nil {
		return nil, err
	}

	// 로그인 타입에서 찾은 유저를 인증합니다.

	response := types.Response{
		Result: []types.Result{
			{
				Code:    epp.EppOk.Code(),
				Message: epp.EppOk.Message(),
			},
		},
		TransactionID: types.TransactionID{
			ServerTransactionID: "ABC-123",
		},
	}

	return epp.Encode(
		response,
		epp.ServerXMLAttributes(),
	)
}

func infoDomainWithExtension(s *epp.Session, data []byte) ([]byte, error) {
	di := types.DomainInfoTypeIn{}

	if err := xml.Unmarshal(data, &di); err != nil {
		return nil, err
	}

	// 도메인을 찾았다고 가정합니다.

	// 기본 데이터로 결과값을 만듭니다.
	diResponse := types.DomainInfoDataType{
		InfoData: types.DomainInfoData{
			Name: di.Info.Name.Name,
			ROID: "DOMAIN_0000000000-SE",
			Status: []types.DomainStatus{
				{
					DomainStatusType: types.DomainStatusOk,
				},
			},
			Host: []string{
				fmt.Sprintf("ns1.%s", di.Info.Name.Name),
				fmt.Sprintf("ns2.%s", di.Info.Name.Name),
			},
			ClientID: "Some Client",
			CreateID: "Some Client",
			UpdateID: "Some Client",
		},
	}

	// Add extension data from extension iis-1.2.
	diIISExtensionResponse := types.IISExtensionInfoDataType{
		InfoData: types.IISExtensionInfoData{
			State:        "active",
			ClientDelete: false,
		},
	}

	// Add extension data from secDNS-1.1.
	diDNSSECExtensionResponse := types.DNSSECExtensionInfoDataType{
		InfoData: types.DNSSECOrKeyData{
			DNSSECData: []types.DNSSEC{
				{
					KeyTag:     10,
					Algorithm:  3,
					DigestType: 5,
					Digest:     "FFAB0102FFAB0102FFAB0102",
				},
			},
		},
	}

	// Generate the response with the default result data and two extensions.
	response := types.Response{
		Result: []types.Result{
			{
				Code:    epp.EppOk.Code(),
				Message: epp.EppOk.Message(),
			},
		},
		ResultData: diResponse,
		// Inline construct an extension type that holds both DNSSEC and IIS.
		Extension: struct {
			types.IISExtensionInfoDataType
			types.DNSSECExtensionInfoDataType
		}{
			diIISExtensionResponse,
			diDNSSECExtensionResponse,
		},
		TransactionID: types.TransactionID{
			ServerTransactionID: "ABC-123",
		},
	}

	return epp.Encode(
		response,
		epp.ServerXMLAttributes(),
	)
}

func createDomain(s *epp.Session, data []byte) ([]byte, error) {
	dc := types.DomainCreateTypeIn{}

	if err := xml.Unmarshal(data, &dc); err != nil {
		return nil, err
	}

	// Do stuff with dc which holds all (validated) domain create data.

	return epp.Encode(
		epp.CreateErrorResponse(epp.EppUnimplementedCommand, "not yet implemented"),
		epp.ServerXMLAttributes(),
	)
}

func createContactWithExtension(s *epp.Session, data []byte) ([]byte, error) {
	cc := struct {
		types.ContactCreate
		types.IISExtensionCreate
	}{}

	if err := xml.Unmarshal(data, &cc); err != nil {
		return nil, err
	}

	// Do stuff with cc which holds all (validated) domain create data.

	return epp.Encode(
		epp.CreateErrorResponse(epp.EppUnimplementedCommand, "not yet implemented"),
		epp.ServerXMLAttributes(),
	)
}

func verifyClientCertificate(certs []*x509.Certificate) error {
	if len(certs) != 1 {
		return errors.New("dind't find one single client ceritficate")
	}

	cert := certs[0]
	_, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("could not convert public key")
	}

	// Do something with public key.
	return nil
}

func generateCertificate() tls.Certificate {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1653),
		Subject: pkix.Name{
			CommonName:   "epp.example.test",
			Organization: []string{"Simple Server Test"},
			Country:      []string{"SE"},
			Locality:     []string{"Stockholm"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, 1),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	certificate, _ := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)

	return tls.Certificate{
		Certificate: [][]byte{certificate},
		PrivateKey:  key,
	}
}
