// Code generated by MockGen. DO NOT EDIT.
// Source: helper.go

// Package sign is a generated GoMock package.
package sign

import (
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	v1beta1 "github.com/rh-ecosystem-edge/kernel-module-management/api/v1beta1"
)

// MockHelper is a mock of Helper interface.
type MockHelper struct {
	ctrl     *gomock.Controller
	recorder *MockHelperMockRecorder
}

// MockHelperMockRecorder is the mock recorder for MockHelper.
type MockHelperMockRecorder struct {
	mock *MockHelper
}

// NewMockHelper creates a new mock instance.
func NewMockHelper(ctrl *gomock.Controller) *MockHelper {
	mock := &MockHelper{ctrl: ctrl}
	mock.recorder = &MockHelperMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockHelper) EXPECT() *MockHelperMockRecorder {
	return m.recorder
}

// GetRelevantSign mocks base method.
func (m *MockHelper) GetRelevantSign(modSpec v1beta1.ModuleSpec, km v1beta1.KernelMapping, kernel string) (*v1beta1.Sign, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetRelevantSign", modSpec, km, kernel)
	ret0, _ := ret[0].(*v1beta1.Sign)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetRelevantSign indicates an expected call of GetRelevantSign.
func (mr *MockHelperMockRecorder) GetRelevantSign(modSpec, km, kernel interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetRelevantSign", reflect.TypeOf((*MockHelper)(nil).GetRelevantSign), modSpec, km, kernel)
}
