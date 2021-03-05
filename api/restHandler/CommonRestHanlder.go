/*
 * Copyright (c) 2020 Devtron Labs
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package restHandler

import (
	"errors"
	"github.com/devtron-labs/devtron/pkg/commonService"
	"github.com/devtron-labs/devtron/pkg/gitops"
	"github.com/devtron-labs/devtron/pkg/user"
	"github.com/devtron-labs/devtron/util/rbac"
	"go.uber.org/zap"
	"gopkg.in/go-playground/validator.v9"
	"net/http"
)

type CommonRestHanlder interface {
	GlobalChecklist(w http.ResponseWriter, r *http.Request)
}

type CommonRestHanlderImpl struct {
	logger              *zap.SugaredLogger
	gitOpsConfigService gitops.GitOpsConfigService
	userAuthService     user.UserService
	validator           *validator.Validate
	enforcer            rbac.Enforcer
	commonService       commonService.CommonService
}

func NewCommonRestHanlderImpl(
	logger *zap.SugaredLogger,
	gitOpsConfigService gitops.GitOpsConfigService, userAuthService user.UserService,
	validator *validator.Validate, enforcer rbac.Enforcer, commonService commonService.CommonService) *CommonRestHanlderImpl {
	return &CommonRestHanlderImpl{
		logger:              logger,
		gitOpsConfigService: gitOpsConfigService,
		userAuthService:     userAuthService,
		validator:           validator,
		enforcer:            enforcer,
		commonService:       commonService,
	}
}

func (impl CommonRestHanlderImpl) GlobalChecklist(w http.ResponseWriter, r *http.Request) {
	userId, err := impl.userAuthService.GetLoggedInUser(r)
	if userId == 0 || err != nil {
		writeJsonResp(w, err, "Unauthorized User", http.StatusUnauthorized)
		return
	}
	res, err := impl.commonService.GlobalChecklist()
	if err != nil {
		impl.logger.Errorw("service err, GlobalChecklist", "err", err)
		writeJsonResp(w, err, nil, http.StatusInternalServerError)
		return
	}

	// RBAC enforcer applying
	token := r.Header.Get("token")
	if ok := impl.enforcer.Enforce(token, rbac.ResourceGlobal, rbac.ActionGet, "*"); !ok {
		writeJsonResp(w, errors.New("unauthorized"), nil, http.StatusForbidden)
		return
	}
	// RBAC enforcer Ends

	writeJsonResp(w, err, res, http.StatusOK)
}