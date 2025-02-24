package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-kit/kit/endpoint"
	"github.com/gorilla/mux"
	moovhttp "github.com/moov-io/base/http"
)

func bindJSON(request *http.Request, params interface{}) (err error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("could not parse json request: %s", err)
	}

	err = json.Unmarshal(body, params)
	if err != nil {
		return fmt.Errorf("could not parse json request: %s", err)
	}
	return
}

type getMachinesRequest struct {
	requestID string
}

type getMachinesResponse struct {
	Machines []*Machine `json:"machines"`
	Err      error      `json:"error"`
}

func decodeGetMachinesRequest(_ context.Context, request *http.Request) (interface{}, error) {
	return getMachinesRequest{
		requestID: moovhttp.GetRequestID(request),
	}, nil
}

func getMachinesEndpoint(s Service) endpoint.Endpoint {
	return func(_ context.Context, _ interface{}) (interface{}, error) {
		return getMachinesResponse{
			Machines: s.GetMachines(),
			Err:      nil,
		}, nil
	}
}

type findMachineRequest struct {
	requestID string
	ik        string
}

type findMachineResponse struct {
	Machine *Machine `json:"machine"`
	Err     error    `json:"error"`
}

func decodeFindMachineRequest(_ context.Context, request *http.Request) (interface{}, error) {
	req := findMachineRequest{
		requestID: moovhttp.GetRequestID(request),
	}

	req.ik = mux.Vars(request)["ik"]
	return req, nil
}
func findMachineEndpoint(s Service) endpoint.Endpoint {
	return func(_ context.Context, request interface{}) (interface{}, error) {
		req, ok := request.(findMachineRequest)
		if !ok {
			return findMachineResponse{Err: ErrFoundABug}, ErrFoundABug
		}

		resp := findMachineResponse{}
		m, err := s.GetMachine(req.ik)
		if err != nil {
			resp.Err = err
			return resp, nil
		}

		resp.Machine = m
		return resp, nil
	}
}

type createMachineRequest struct {
	vaultAuth Vault
	requestID string
}

type createMachineResponse struct {
	IK      string   `json:"ik"`
	Machine *Machine `json:"machine"`
	Err     error    `json:"error"`
}

func decodeCreateMachineRequest(_ context.Context, request *http.Request) (interface{}, error) {
	req := createMachineRequest{
		requestID: moovhttp.GetRequestID(request),
	}

	if err := bindJSON(request, &req.vaultAuth); err != nil {
		return nil, err
	}

	return req, nil
}

func createMachineEndpoint(s Service) endpoint.Endpoint {
	return func(_ context.Context, request interface{}) (interface{}, error) {
		req, ok := request.(createMachineRequest)
		if req.vaultAuth.VaultAddress == "" {
			return createMachineResponse{Err: ErrFoundABug}, errInvalidVaultAddress
		}
		if req.vaultAuth.VaultToken == "" {
			return createMachineResponse{Err: ErrFoundABug}, errInvalidVaultToken
		}
		if IsValidURL(req.vaultAuth.VaultAddress) == false {
			return createMachineResponse{Err: ErrFoundABug}, errInvalidVaultAddress
		}
		if !ok {
			return createMachineResponse{Err: ErrFoundABug}, ErrFoundABug
		}

		resp := createMachineResponse{}

		m := NewMachine(req.vaultAuth)
		err := s.CreateMachine(m)
		if err != nil {
			resp.Err = err
			return resp, err
		}

		resp.Machine = m
		resp.IK = m.InitialKey

		return resp, nil
	}
}

type encryptDataRequest struct {
	requestID  string
	ik         string
	keyPath    string
	keyName    string
	encryptKey string
	header     HeaderParams
	timeout    time.Duration
}
type encryptDataResponse struct {
	Data string `json:"data"`
	Err  error  `json:"error"`
}

func decodeEncryptDataRequest(_ context.Context, request *http.Request) (interface{}, error) {
	req := encryptDataRequest{
		requestID: moovhttp.GetRequestID(request),
	}
	req.ik = mux.Vars(request)["ik"]
	type requestParam struct {
		keyPath    string
		keyName    string
		encryptKey string
		header     HeaderParams
		timeout    time.Duration
	}
	reqParams := requestParam{}
	if err := bindJSON(request, &reqParams); err != nil {
		return nil, err
	}

	req.keyPath = reqParams.keyPath
	req.keyName = reqParams.keyName
	req.encryptKey = reqParams.encryptKey
	req.header = reqParams.header
	req.timeout = reqParams.timeout
	return req, nil
}

func encryptDataEndpoint(s Service) endpoint.Endpoint {
	return func(_ context.Context, request interface{}) (interface{}, error) {
		req, ok := request.(encryptDataRequest)
		if !ok {
			return encryptDataResponse{Err: ErrFoundABug}, ErrFoundABug
		}

		resp := encryptDataResponse{}
		encrypted, err := s.EncryptData(req.ik, req.keyPath, req.keyName, req.encryptKey, req.header, req.timeout)
		if err != nil {
			resp.Err = err
			return resp, nil
		}

		resp.Data = encrypted
		return resp, nil
	}
}

type decryptDataRequest struct {
	requestID string
	ik        string
	keyPath   string
	keyName   string
	keyBlock  string
	timeout   time.Duration
}
type decryptDataResponse struct {
	Data string `json:"data"`
	Err  error  `json:"error"`
}

func decodeDecryptDataRequest(_ context.Context, request *http.Request) (interface{}, error) {
	req := decryptDataRequest{
		requestID: moovhttp.GetRequestID(request),
	}

	req.ik = mux.Vars(request)["ik"]

	type requestParam struct {
		keyPath  string
		keyName  string
		keyBlock string
		timeout  time.Duration
	}

	reqParams := requestParam{}
	if err := bindJSON(request, &reqParams); err != nil {
		return nil, err
	}

	req.keyPath = reqParams.keyPath
	req.keyName = reqParams.keyName
	req.keyBlock = reqParams.keyBlock
	req.timeout = reqParams.timeout
	return req, nil
}

func decryptDataEndpoint(s Service) endpoint.Endpoint {
	return func(_ context.Context, request interface{}) (interface{}, error) {
		req, ok := request.(decryptDataRequest)
		if !ok {
			return decryptDataResponse{Err: ErrFoundABug}, ErrFoundABug
		}

		resp := decryptDataResponse{}
		decrypted, err := s.DecryptData(req.ik, req.keyPath, req.keyName, req.keyBlock, req.timeout)
		if err != nil {
			resp.Err = err
			return resp, nil
		}

		resp.Data = decrypted
		return resp, nil
	}
}
