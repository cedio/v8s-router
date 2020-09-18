/*
Copyright 2020 cedio.

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

package controllers

import (
	"errors"
	"fmt"
)

type (
	State     = func(...interface{}) error
	States    = map[string]State
	Condition = func(*Proflow, ...interface{}) error
	Class     = func(State, ...interface{}) error
)

type Proflow struct {
	Name      string
	apis      []interface{}
	states    States
	condition Condition
	class     Class
}

var (
	EmptyClass     Class     = func(State, ...interface{}) error { return nil }
	EmptyCondition Condition = func(*Proflow, ...interface{}) error { return nil }
	EmptyState     State     = func(...interface{}) error { return nil }
)

func (p *Proflow) InitClass(class Class) *Proflow {
	p.class = class
	return p
}

func (p *Proflow) InitCondition(condition Condition) *Proflow {
	p.condition = condition
	return p
}

func (p *Proflow) Init(class Class, condition Condition) *Proflow {
	return p.InitClass(class).InitCondition(condition)
}

func (p *Proflow) SetState(name string, state State) *Proflow {
	// Initialize states
	if p.states == nil {
		p.states = make(States)
	}
	p.states[name] = state
	return p
}

func (p *Proflow) SetAPI(apis ...interface{}) *Proflow {
	p.apis = apis
	return p
}

func (p *Proflow) ApplyClass(stateName string) error {
	if state, ok := p.states[stateName]; ok {
		if p.class != nil {
			return p.class(state, p.apis...)
		}
		return newProflowError("Class not initialized")
	}
	return newProflowError("State not found in proflow.states")
}

func (p *Proflow) Apply() error {
	return p.condition(p, p.apis...)
}

func newProflowError(message string) error {
	return fmt.Errorf("Exception in proflow :: %s", message)
}

func NewProflowRuntimeError(message string) error {
	return errors.New(message)
}
