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

package appStoreDeployment

import (
	"context"
	"fmt"
	"github.com/devtron-labs/devtron/internal/constants"
	"github.com/devtron-labs/devtron/internal/sql/repository/app"
	"github.com/devtron-labs/devtron/internal/util"
	appStoreBean "github.com/devtron-labs/devtron/pkg/appStore/bean"
	appStoreDeploymentTool "github.com/devtron-labs/devtron/pkg/appStore/deployment/tool"
	appStoreDeploymentGitopsTool "github.com/devtron-labs/devtron/pkg/appStore/deployment/tool/gitops"
	appStoreDiscoverRepository "github.com/devtron-labs/devtron/pkg/appStore/discover/repository"
	appStoreRepository "github.com/devtron-labs/devtron/pkg/appStore/repository"
	"github.com/devtron-labs/devtron/pkg/bean"
	"github.com/devtron-labs/devtron/pkg/cluster"
	cluster2 "github.com/devtron-labs/devtron/pkg/cluster"
	clusterRepository "github.com/devtron-labs/devtron/pkg/cluster/repository"
	"github.com/devtron-labs/devtron/pkg/sql"
	util2 "github.com/devtron-labs/devtron/util"
	"github.com/go-pg/pg"
	"go.uber.org/zap"
	"net/http"
	"time"
)

type AppStoreDeploymentService interface {
	AppStoreDeployOperationDB(installAppVersionRequest *appStoreBean.InstallAppVersionDTO, tx *pg.Tx) (*appStoreBean.InstallAppVersionDTO, error)
	AppStoreDeployOperationStatusUpdate(installAppId int, status appStoreBean.AppstoreDeploymentStatus) (bool, error)
	IsChartRepoActive(appStoreVersionId int) (bool, error)
	InstallApp(installAppVersionRequest *appStoreBean.InstallAppVersionDTO, ctx context.Context) (*appStoreBean.InstallAppVersionDTO, error)
	GetInstalledApp(id int) (*appStoreBean.InstallAppVersionDTO, error)
	GetAllInstalledAppsByAppStoreId(w http.ResponseWriter, r *http.Request, token string, appStoreId int) ([]appStoreBean.InstalledAppsResponse, error)
	DeleteInstalledApp(ctx context.Context, installAppVersionRequest *appStoreBean.InstallAppVersionDTO) (*appStoreBean.InstallAppVersionDTO, error)
}

type AppStoreDeploymentServiceImpl struct {
	logger                               *zap.SugaredLogger
	installedAppRepository               appStoreRepository.InstalledAppRepository
	appStoreApplicationVersionRepository appStoreDiscoverRepository.AppStoreApplicationVersionRepository
	environmentRepository                clusterRepository.EnvironmentRepository
	clusterInstalledAppsRepository       appStoreRepository.ClusterInstalledAppsRepository
	appRepository                        app.AppRepository
	appStoreDeploymentHelmService        appStoreDeploymentTool.AppStoreDeploymentHelmService
	appStoreDeploymentArgoCdService      appStoreDeploymentGitopsTool.AppStoreDeploymentArgoCdService
	environmentService                   cluster.EnvironmentService
	clusterService                       cluster.ClusterService
}

func NewAppStoreDeploymentServiceImpl(logger *zap.SugaredLogger, installedAppRepository appStoreRepository.InstalledAppRepository,
	appStoreApplicationVersionRepository appStoreDiscoverRepository.AppStoreApplicationVersionRepository, environmentRepository clusterRepository.EnvironmentRepository,
	clusterInstalledAppsRepository appStoreRepository.ClusterInstalledAppsRepository, appRepository app.AppRepository,
	appStoreDeploymentHelmService appStoreDeploymentTool.AppStoreDeploymentHelmService,
	appStoreDeploymentArgoCdService appStoreDeploymentGitopsTool.AppStoreDeploymentArgoCdService, environmentService cluster.EnvironmentService,
	clusterService cluster.ClusterService) *AppStoreDeploymentServiceImpl {
	return &AppStoreDeploymentServiceImpl{
		logger:                               logger,
		installedAppRepository:               installedAppRepository,
		appStoreApplicationVersionRepository: appStoreApplicationVersionRepository,
		environmentRepository:                environmentRepository,
		clusterInstalledAppsRepository:       clusterInstalledAppsRepository,
		appRepository:                        appRepository,
		appStoreDeploymentHelmService:        appStoreDeploymentHelmService,
		appStoreDeploymentArgoCdService:      appStoreDeploymentArgoCdService,
		environmentService:                   environmentService,
		clusterService:                       clusterService,
	}
}

func (impl AppStoreDeploymentServiceImpl) AppStoreDeployOperationDB(installAppVersionRequest *appStoreBean.InstallAppVersionDTO, tx *pg.Tx) (*appStoreBean.InstallAppVersionDTO, error) {

	appStoreAppVersion, err := impl.appStoreApplicationVersionRepository.FindById(installAppVersionRequest.AppStoreVersion)
	if err != nil {
		impl.logger.Errorw("fetching error", "err", err)
		return nil, err
	}

	// create env if env not exists for clusterId and namespace for hyperion mode
	if util2.GetDevtronVersion().ServerMode == util2.SERVER_MODE_HYPERION {
		envId, err := impl.createEnvironmentIfNotExists(installAppVersionRequest)
		if err != nil {
			return nil, err
		}
		installAppVersionRequest.EnvironmentId = envId
	}

	environment, err := impl.environmentRepository.FindById(installAppVersionRequest.EnvironmentId)
	if err != nil {
		impl.logger.Errorw("fetching error", "err", err)
		return nil, err
	}

	appCreateRequest := &bean.CreateAppDTO{
		Id:      installAppVersionRequest.AppId,
		AppName: installAppVersionRequest.AppName,
		TeamId:  installAppVersionRequest.TeamId,
		UserId:  installAppVersionRequest.UserId,
	}

	appCreateRequest, err = impl.createAppForAppStore(appCreateRequest, tx)
	if err != nil {
		impl.logger.Errorw("error while creating app", "error", err)
		return nil, err
	}
	installAppVersionRequest.AppId = appCreateRequest.Id

	installedAppModel := &appStoreRepository.InstalledApps{
		AppId:         appCreateRequest.Id,
		EnvironmentId: environment.Id,
		Status:        appStoreBean.DEPLOY_INIT,
	}
	installedAppModel.CreatedBy = installAppVersionRequest.UserId
	installedAppModel.UpdatedBy = installAppVersionRequest.UserId
	installedAppModel.CreatedOn = time.Now()
	installedAppModel.UpdatedOn = time.Now()
	installedAppModel.Active = true
	installedApp, err := impl.installedAppRepository.CreateInstalledApp(installedAppModel, tx)
	if err != nil {
		impl.logger.Errorw("error while creating install app", "error", err)
		return nil, err
	}
	installAppVersionRequest.InstalledAppId = installedApp.Id

	installedAppVersions := &appStoreRepository.InstalledAppVersions{
		InstalledAppId:               installAppVersionRequest.InstalledAppId,
		AppStoreApplicationVersionId: appStoreAppVersion.Id,
		ValuesYaml:                   installAppVersionRequest.ValuesOverrideYaml,
		//Values:                       "{}",
	}
	installedAppVersions.CreatedBy = installAppVersionRequest.UserId
	installedAppVersions.UpdatedBy = installAppVersionRequest.UserId
	installedAppVersions.CreatedOn = time.Now()
	installedAppVersions.UpdatedOn = time.Now()
	installedAppVersions.Active = true
	installedAppVersions.ReferenceValueId = installAppVersionRequest.ReferenceValueId
	installedAppVersions.ReferenceValueKind = installAppVersionRequest.ReferenceValueKind
	_, err = impl.installedAppRepository.CreateInstalledAppVersion(installedAppVersions, tx)
	if err != nil {
		impl.logger.Errorw("error while fetching from db", "error", err)
		return nil, err
	}
	installAppVersionRequest.InstalledAppVersionId = installedAppVersions.Id

	if installAppVersionRequest.DefaultClusterComponent {
		clusterInstalledAppsModel := &appStoreRepository.ClusterInstalledApps{
			ClusterId:      environment.ClusterId,
			InstalledAppId: installAppVersionRequest.InstalledAppId,
		}
		clusterInstalledAppsModel.CreatedBy = installAppVersionRequest.UserId
		clusterInstalledAppsModel.UpdatedBy = installAppVersionRequest.UserId
		clusterInstalledAppsModel.CreatedOn = time.Now()
		clusterInstalledAppsModel.UpdatedOn = time.Now()
		err = impl.clusterInstalledAppsRepository.Save(clusterInstalledAppsModel, tx)
		if err != nil {
			impl.logger.Errorw("error while creating cluster install app", "error", err)
			return nil, err
		}
	}
	return installAppVersionRequest, nil
}

func (impl AppStoreDeploymentServiceImpl) AppStoreDeployOperationStatusUpdate(installAppId int, status appStoreBean.AppstoreDeploymentStatus) (bool, error) {
	dbConnection := impl.installedAppRepository.GetConnection()
	tx, err := dbConnection.Begin()
	if err != nil {
		return false, err
	}
	// Rollback tx on error.
	defer tx.Rollback()
	installedApp, err := impl.installedAppRepository.GetInstalledApp(installAppId)
	if err != nil {
		impl.logger.Errorw("error while fetching from db", "error", err)
		return false, err
	}
	installedApp.Status = status
	_, err = impl.installedAppRepository.UpdateInstalledApp(installedApp, tx)
	if err != nil {
		impl.logger.Errorw("error while fetching from db", "error", err)
		return false, err
	}
	err = tx.Commit()
	if err != nil {
		impl.logger.Errorw("error while commit db transaction to db", "error", err)
		return false, err
	}
	return true, nil
}

func (impl *AppStoreDeploymentServiceImpl) IsChartRepoActive(appStoreVersionId int) (bool, error) {
	appStoreAppVersion, err := impl.appStoreApplicationVersionRepository.FindById(appStoreVersionId)
	if err != nil {
		impl.logger.Errorw("fetching error", "err", err)
		return false, err
	}
	return appStoreAppVersion.AppStore.ChartRepo.Active, nil
}

func (impl AppStoreDeploymentServiceImpl) InstallApp(installAppVersionRequest *appStoreBean.InstallAppVersionDTO, ctx context.Context) (*appStoreBean.InstallAppVersionDTO, error) {

	dbConnection := impl.installedAppRepository.GetConnection()
	tx, err := dbConnection.Begin()
	if err != nil {
		return nil, err
	}
	// Rollback tx on error.
	defer tx.Rollback()

	//step 1 db operation initiated
	installAppVersionRequest, err = impl.AppStoreDeployOperationDB(installAppVersionRequest, tx)
	if err != nil {
		impl.logger.Errorw(" error", "err", err)
		return nil, err
	}

	if util2.GetDevtronVersion().ServerMode == util2.SERVER_MODE_HYPERION {
		err = impl.appStoreDeploymentHelmService.InstallApp(installAppVersionRequest, ctx)
	} else {
		err = impl.appStoreDeploymentArgoCdService.InstallApp(installAppVersionRequest, ctx)
	}

	if err != nil {
		return nil, err
	}

	// tx commit here because next operation will be process after this commit.
	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	//step 4 db operation status update to deploy success
	_, err = impl.AppStoreDeployOperationStatusUpdate(installAppVersionRequest.InstalledAppId, appStoreBean.DEPLOY_SUCCESS)
	if err != nil {
		impl.logger.Errorw(" error", "err", err)
		return nil, err
	}

	return installAppVersionRequest, nil
}

func (impl AppStoreDeploymentServiceImpl) createAppForAppStore(createRequest *bean.CreateAppDTO, tx *pg.Tx) (*bean.CreateAppDTO, error) {
	app1, err := impl.appRepository.FindActiveByName(createRequest.AppName)
	if err != nil && err != pg.ErrNoRows {
		return nil, err
	}
	if app1 != nil && app1.Id > 0 {
		impl.logger.Infow(" app already exists", "name", createRequest.AppName)
		err = &util.ApiError{
			Code:            constants.AppAlreadyExists.Code,
			InternalMessage: "app already exists",
			UserMessage:     fmt.Sprintf("app already exists with name %s", createRequest.AppName),
		}
		return nil, err
	}
	pg := &app.App{
		Active:          true,
		AppName:         createRequest.AppName,
		TeamId:          createRequest.TeamId,
		AppStore:        true,
		AppOfferingMode: util2.GetDevtronVersion().ServerMode,
		AuditLog:        sql.AuditLog{UpdatedBy: createRequest.UserId, CreatedBy: createRequest.UserId, UpdatedOn: time.Now(), CreatedOn: time.Now()},
	}
	err = impl.appRepository.SaveWithTxn(pg, tx)
	if err != nil {
		impl.logger.Errorw("error in saving entity ", "entity", pg)
		return nil, err
	}

	// if found more than 1 application, soft delete all except first item
	apps, err := impl.appRepository.FindActiveListByName(createRequest.AppName)
	if err != nil {
		return nil, err
	}
	appLen := len(apps)
	if appLen > 1 {
		firstElement := apps[0]
		if firstElement.Id != pg.Id {
			pg.Active = false
			err = impl.appRepository.UpdateWithTxn(pg, tx)
			if err != nil {
				impl.logger.Errorw("error in saving entity ", "entity", pg)
				return nil, err
			}
			err = &util.ApiError{
				Code:            constants.AppAlreadyExists.Code,
				InternalMessage: "app already exists",
				UserMessage:     fmt.Sprintf("app already exists with name %s", createRequest.AppName),
			}
			return nil, err
		}
	}

	createRequest.Id = pg.Id
	return createRequest, nil
}

func (impl AppStoreDeploymentServiceImpl) GetInstalledApp(id int) (*appStoreBean.InstallAppVersionDTO, error) {

	app, err := impl.installedAppRepository.GetInstalledApp(id)
	if err != nil {
		impl.logger.Errorw("error while fetching from db", "error", err)
		return nil, err
	}
	chartTemplate := impl.chartAdaptor2(app)
	return chartTemplate, nil
}

//converts db object to bean
func (impl AppStoreDeploymentServiceImpl) chartAdaptor2(chart *appStoreRepository.InstalledApps) *appStoreBean.InstallAppVersionDTO {
	return &appStoreBean.InstallAppVersionDTO{
		EnvironmentId:   chart.EnvironmentId,
		Id:              chart.Id,
		AppId:           chart.AppId,
		AppOfferingMode: chart.App.AppOfferingMode,
		ClusterId:       chart.Environment.ClusterId,
		Namespace:       chart.Environment.Namespace,
		AppName:         chart.App.AppName,
		EnvironmentName: chart.Environment.Name,
	}
}

func (impl AppStoreDeploymentServiceImpl) GetAllInstalledAppsByAppStoreId(w http.ResponseWriter, r *http.Request, token string, appStoreId int) ([]appStoreBean.InstalledAppsResponse, error) {
	installedApps, err := impl.installedAppRepository.GetAllIntalledAppsByAppStoreId(appStoreId)
	if err != nil && !util.IsErrNoRows(err) {
		impl.logger.Error(err)
		return nil, err
	}
	var installedAppsEnvResponse []appStoreBean.InstalledAppsResponse
	for _, a := range installedApps {
		var status string
		if util2.GetDevtronVersion().ServerMode == util2.SERVER_MODE_HYPERION || a.AppOfferingMode == util2.SERVER_MODE_HYPERION  {
			status, err = impl.appStoreDeploymentHelmService.GetAppStatus(a, w, r, token)
		}else{
			status, err = impl.appStoreDeploymentArgoCdService.GetAppStatus(a, w, r, token)
		}
		if apiErr, ok := err.(*util.ApiError); ok {
			if apiErr.Code == constants.AppDetailResourceTreeNotFound {
				status = "Not Found"
			}
		} else if err != nil {
			impl.logger.Error(err)
			return nil, err
		}
		installedAppRes := appStoreBean.InstalledAppsResponse{
			EnvironmentName:              a.EnvironmentName,
			AppName:                      a.AppName,
			DeployedAt:                   a.UpdatedOn,
			DeployedBy:                   a.EmailId,
			Status:                       status,
			AppStoreApplicationVersionId: a.AppStoreApplicationVersionId,
			InstalledAppVersionId:        a.InstalledAppVersionId,
			InstalledAppsId:              a.InstalledAppId,
			EnvironmentId:                a.EnvironmentId,
			AppOfferingMode:              a.AppOfferingMode,
		}

		// if hyperion mode app, then fill clusterId and namespace
		if a.AppOfferingMode == util2.SERVER_MODE_HYPERION {
			environment, err := impl.environmentRepository.FindById(a.EnvironmentId)
			if err != nil {
				impl.logger.Errorw("fetching environment error", "err", err)
				return nil, err
			}
			installedAppRes.ClusterId = environment.ClusterId
			installedAppRes.Namespace = environment.Namespace
		}

		installedAppsEnvResponse = append(installedAppsEnvResponse, installedAppRes)
	}
	return installedAppsEnvResponse, nil
}

func (impl AppStoreDeploymentServiceImpl) DeleteInstalledApp(ctx context.Context, installAppVersionRequest *appStoreBean.InstallAppVersionDTO) (*appStoreBean.InstallAppVersionDTO, error) {

	environment, err := impl.environmentRepository.FindById(installAppVersionRequest.EnvironmentId)
	if err != nil {
		impl.logger.Errorw("fetching error", "err", err)
		return nil, err
	}

	dbConnection := impl.installedAppRepository.GetConnection()
	tx, err := dbConnection.Begin()
	if err != nil {
		return nil, err
	}
	// Rollback tx on error.
	defer tx.Rollback()

	app, err := impl.appRepository.FindById(installAppVersionRequest.AppId)
	if err != nil {
		return nil, err
	}
	app.Active = false
	app.UpdatedBy = installAppVersionRequest.UserId
	app.UpdatedOn = time.Now()
	err = impl.appRepository.UpdateWithTxn(app, tx)
	if err != nil {
		impl.logger.Errorw("error in update entity ", "entity", app)
		return nil, err
	}

	model, err := impl.installedAppRepository.GetInstalledApp(installAppVersionRequest.InstalledAppId)
	if err != nil {
		impl.logger.Errorw("error in fetching installed app", "id", installAppVersionRequest.InstalledAppId, "err", err)
		return nil, err
	}
	model.Active = false
	model.UpdatedBy = installAppVersionRequest.UserId
	model.UpdatedOn = time.Now()
	_, err = impl.installedAppRepository.UpdateInstalledApp(model, tx)
	if err != nil {
		impl.logger.Errorw("error while creating install app", "error", err)
		return nil, err
	}
	models, err := impl.installedAppRepository.GetInstalledAppVersionByInstalledAppId(installAppVersionRequest.InstalledAppId)
	if err != nil {
		impl.logger.Errorw("error while fetching install app versions", "error", err)
		return nil, err
	}
	for _, item := range models {
		item.Active = false
		item.UpdatedBy = installAppVersionRequest.UserId
		item.UpdatedOn = time.Now()
		_, err = impl.installedAppRepository.UpdateInstalledAppVersion(item, tx)
		if err != nil {
			impl.logger.Errorw("error while fetching from db", "error", err)
			return nil, err
		}
	}

	if util2.GetDevtronVersion().ServerMode == util2.SERVER_MODE_HYPERION || app.AppOfferingMode == util2.SERVER_MODE_HYPERION  {
		err = impl.appStoreDeploymentHelmService.DeleteInstalledApp(ctx, app.AppName, environment.Name, installAppVersionRequest, model, tx)
	}else{
		err = impl.appStoreDeploymentArgoCdService.DeleteInstalledApp(ctx, app.AppName, environment.Name, installAppVersionRequest, model, tx)
	}

	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		impl.logger.Errorw("error in commit db transaction on delete", "err", err)
		return nil, err
	}

	return installAppVersionRequest, nil
}

func (impl AppStoreDeploymentServiceImpl) createEnvironmentIfNotExists(installAppVersionRequest *appStoreBean.InstallAppVersionDTO) (int, error) {
	clusterId := installAppVersionRequest.ClusterId
	namespace := installAppVersionRequest.Namespace
	env, err := impl.environmentRepository.FindOneByNamespaceAndClusterId(namespace, clusterId)

	if err == nil {
		return env.Id, nil
	}

	if err != pg.ErrNoRows {
		return 0, err
	}

	// create env
	cluster, err := impl.clusterService.FindById(clusterId)
	if err != nil {
		return 0, err
	}

	environmentBean := &cluster2.EnvironmentBean{
		Environment: cluster2.BuildEnvironmentIdentifer(cluster.ClusterName, namespace),
		ClusterId:   clusterId,
		Namespace:   namespace,
		Default:     false,
		Active:      true,
	}
	envCreateRes, err := impl.environmentService.Create(environmentBean, installAppVersionRequest.UserId)
	if err != nil {
		return 0, err
	}

	return envCreateRes.Id, nil
}
