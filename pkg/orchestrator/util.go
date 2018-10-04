/*
Copyright 2018 Pressinfra SRL

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"

	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("orchestrator.util")

type orcError struct {
	HTTPStatus int
	Message    string
	Details    interface{}
}

func (e orcError) Error() string {
	return fmt.Sprintf("[orc]: status: %d msg: %s, details: %v",
		e.HTTPStatus, e.Message, e.Details)
}

// NewOrcError returns a specific orchestrator error with extra details
func NewOrcError(resp *http.Response) error {
	rsp := orcError{
		HTTPStatus: resp.StatusCode,
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		rsp.Message = "Can't read body"
		return rsp
	}

	if err = json.Unmarshal(body, &rsp); err != nil {
		log.V(3).Info("unmarshal data error", "body", body)
		rsp.Message = fmt.Sprintf("at error, json unmarshal error: %s", err)
		return rsp
	}

	return rsp
}

// NewOrcErrorMsg returns an orchestrator error with extra msg
func NewOrcErrorMsg(msg string) error {
	return orcError{
		HTTPStatus: 0,
		Message:    msg,
	}
}

func (o *orchestrator) makeGetRequest(path string, out interface{}) error {
	uri := fmt.Sprintf("%s/%s", o.connectURI, path)
	log.V(2).Info("orchestrator request info", "uri", uri, "outobj", out)

	resp, err := http.Get(uri)
	if err != nil {
		return NewOrcErrorMsg(err.Error())
	}

	if err := unmarshalJSON(resp.Body, out); err != nil {
		return NewOrcError(resp)
	}

	if resp.StatusCode >= 500 {
		return NewOrcError(resp)
	}

	return nil
}

func (o *orchestrator) makeGetAPIRequest(path string, query map[string][]string) error {
	args := url.Values(query).Encode()
	if len(args) != 0 {
		args = "?" + args
	}

	path = fmt.Sprintf("%s%s", path, args)
	var apiObj struct {
		Code    string
		Message string
	}
	if err := o.makeGetRequest(path, &apiObj); err != nil {
		return err
	}

	if apiObj.Code != "OK" {
		return fmt.Errorf("orc failed with: %s", apiObj.Message)
	}

	return nil
}

func unmarshalJSON(in io.Reader, obj interface{}) error {
	body, err := ioutil.ReadAll(in)
	if err != nil {
		return fmt.Errorf("io read error: %s", err)
	}

	if err = json.Unmarshal(body, obj); err != nil {
		log.V(4).Info("unmarshal error", "body", body)
		return err
	}

	return nil
}
