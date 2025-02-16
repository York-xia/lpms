package service

import (
	"lpms/app/repositories"
	"lpms/app/response"
	"lpms/app/vo"
	"lpms/commom/drivers/database"
	"lpms/constant"
	"lpms/exception"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"
)

var (
	reserveServiceInstance ReserveService
	reserveOnce            sync.Once
)

type reserveServiceImpl struct {
	db       *gorm.DB
	repo     repositories.ReserveRepo
	objRepo  repositories.ObjectRepo
	userRepo repositories.UserRepo
}

func GetReserveService() ReserveService {
	reserveOnce.Do(func() {
		reserveServiceInstance = &reserveServiceImpl{
			db:       database.GetDriver(),
			repo:     repositories.GetReserveRepo(),
			objRepo:  repositories.GetObjectRepo(),
			userRepo: repositories.GetUserRepo(),
		}
	})
	return reserveServiceInstance
}

type ReserveService interface {
	Create(openID string, param *vo.ReserveReq) exception.Exception
	Get(id int64) (*vo.ReserveResp, exception.Exception)
	List(user string, params *vo.ReserveFilterParam, pageInfo *vo.PageInfo) (*vo.DataPagination, exception.Exception)
	Update(openID string, id int64, param *vo.ReserveUpdateReq) exception.Exception
	Delete(id int64) exception.Exception
	MultiDelete(ids string) exception.Exception
	Refer(openID string, id int64) exception.Exception
	Submission(openID string, id int64, req *vo.SubmissionOutStorage) exception.Exception
	MultiSubmission(openID string, ids string) exception.Exception
	OutStorage(openID string, id int64, req *vo.SubmissionOutStorage) exception.Exception
	DataAnalysis(params *vo.ReserveAnalysisFilter) ([]vo.ReserveAnalysisResp, exception.Exception)
}

func (rsi *reserveServiceImpl) Create(openID string, param *vo.ReserveReq) exception.Exception {
	reserve := param.ToModel(openID)
	return rsi.repo.Create(rsi.db, reserve)
}

func (rsi *reserveServiceImpl) Get(id int64) (*vo.ReserveResp, exception.Exception) {
	reserve, ex := rsi.repo.Get(rsi.db, id)
	if ex != nil {
		return nil, ex
	}
	// investInfo, ex := rsi.repo.GetInvestDetail(rsi.db, id)
	// if ex != nil {
	// 	return nil, ex
	// }
	resp, err := vo.NewReserveProResponse(reserve)
	if err != nil {
		return nil, exception.Wrap(response.ExceptionUnmarshalJSON, err)
	}

	return resp, nil
}

func (rsi *reserveServiceImpl) List(user string, params *vo.ReserveFilterParam, pageInfo *vo.PageInfo) (*vo.DataPagination, exception.Exception) {
	userInfo, ex := rsi.userRepo.Get(rsi.db, user)
	if ex != nil {
		return nil, ex
	}
	count, projects, ex := rsi.repo.List(rsi.db, pageInfo, params, userInfo.IsAdmin, user)
	if ex != nil {
		return nil, ex
	}
	resp := make([]vo.ListReserveProResp, 0, len(projects))
	for i := range projects {
		resp = append(resp, vo.ListReserveProResp{
			ID:               projects[i].ID,
			Name:             projects[i].Name,
			Level:            projects[i].Level,
			ProjectType:      projects[i].ProjectType,
			ConstructSubject: projects[i].ConstructSubject,
			CreateAt:         projects[i].CreateAt,
			Status:           projects[i].Status,
		})
	}
	return vo.NewDataPagination(count, resp, pageInfo), nil
}

func (rsi *reserveServiceImpl) Update(openID string, id int64, param *vo.ReserveUpdateReq) exception.Exception {
	pro, ex := rsi.repo.Get(rsi.db, id)
	if ex != nil {
		return ex
	}
	// obj change
	if pro.UploadCadID != "" && pro.UploadCadID != param.UploadCadID {
		ex := rsi.objRepo.Delete(rsi.db, pro.UploadCadID)
		if ex != nil {
			return ex
		}
	}
	if pro.SitePhoto != "" && pro.SitePhoto != param.SitePhoto {
		ex := rsi.objRepo.Delete(rsi.db, pro.SitePhoto)
		if ex != nil {
			return ex
		}
	}
	return rsi.repo.Update(rsi.db, id, param.ToMap(openID))
}

func (rsi *reserveServiceImpl) Delete(id int64) exception.Exception {
	pro, ex := rsi.repo.Get(rsi.db, id)
	if ex != nil {
		return ex
	}
	if pro.SitePhoto != "" {
		if exx := rsi.objRepo.Delete(rsi.db, pro.SitePhoto); exx != nil {
			return exx
		}
	}
	if pro.UploadCadID != "" {
		if exx := rsi.objRepo.Delete(rsi.db, pro.UploadCadID); exx != nil {
			return exx
		}
	}
	return rsi.repo.Delete(rsi.db, id)
}

func (rsi *reserveServiceImpl) MultiDelete(ids string) exception.Exception {
	idslice := strings.Split(ids, ",")
	if len(idslice) == 0 {
		return exception.New(response.ExceptionInvalidRequestParameters, "无效参数")
	}
	did := make([]int64, 0, len(idslice))
	for i := range idslice {
		id, err := strconv.ParseUint(idslice[i], 10, 0)
		if err != nil {
			return exception.Wrap(response.ExceptionParseStringToInt64Error, err)
		}
		did = append(did, int64(id))
	}
	for i := range did {
		pro, ex := rsi.repo.Get(rsi.db, did[i])
		if ex != nil {
			return ex
		}
		if pro.SitePhoto != "" {
			if exx := rsi.objRepo.Delete(rsi.db, pro.SitePhoto); exx != nil {
				return exx
			}
		}
		if pro.UploadCadID != "" {
			if exx := rsi.objRepo.Delete(rsi.db, pro.UploadCadID); exx != nil {
				return exx
			}
		}
	}
	return rsi.repo.MultiDelete(rsi.db, did)
}

func (rsi *reserveServiceImpl) Refer(openID string, id int64) exception.Exception {
	return rsi.repo.Refer(rsi.db, id, map[string]interface{}{
		"update_by": openID,
		"status":    constant.EnteredDB,
	})
}

func (rsi *reserveServiceImpl) Submission(openID string, id int64, req *vo.SubmissionOutStorage) exception.Exception {
	return rsi.repo.Submission(rsi.db, id, map[string]interface{}{
		"update_by":      openID,
		"status":         constant.EarlyPlan,
		"is_case_finish": req.IsCaseFinish,
		"is_research":    req.IsResearch,
	})
}

func (rsi *reserveServiceImpl) MultiSubmission(openID string, ids string) exception.Exception {
	idslice := strings.Split(ids, ",")
	if len(idslice) == 0 {
		return exception.New(response.ExceptionInvalidRequestParameters, "无效参数")
	}
	did := make([]int64, 0, len(idslice))
	for i := range idslice {
		id, err := strconv.ParseUint(idslice[i], 10, 0)
		if err != nil {
			return exception.Wrap(response.ExceptionParseStringToInt64Error, err)
		}
		did = append(did, int64(id))
	}
	return rsi.repo.MultiSubmission(rsi.db, did, map[string]interface{}{
		"update_by": openID,
		"status":    constant.EarlyPlan,
	})
}

func (rsi *reserveServiceImpl) OutStorage(openID string, id int64, req *vo.SubmissionOutStorage) exception.Exception {
	return rsi.repo.OutStorage(rsi.db, id, map[string]interface{}{
		"update_by":      openID,
		"status":         constant.OutStorageInspect,
		"is_case_finish": req.IsCaseFinish,
		"is_research":    req.IsResearch,
	})
}

func (rsi *reserveServiceImpl) DataAnalysis(params *vo.ReserveAnalysisFilter) ([]vo.ReserveAnalysisResp, exception.Exception) {
	res, ex := rsi.repo.DataAnalysis(rsi.db, params)
	if ex != nil {
		return nil, ex
	}
	dataMap := make(map[string][]vo.AnalysisData)
	for i := range res {
		dataMap[res[i].Bucket] = append(dataMap[res[i].Bucket], vo.AnalysisData{Status: res[i].Status, Count: res[i].Count})
	}
	resp := make([]vo.ReserveAnalysisResp, 0, len(res))
	for key, value := range dataMap {
		total := int64(0)
		for i := range value {
			total += value[i].Count
		}
		resp = append(resp, vo.ReserveAnalysisResp{
			Bucket: key,
			Total:  total,
			Data:   value,
		})
	}
	return resp, ex
}
