package core

import (
	"gonum.org/v1/gonum/floats"
	"math"
)

// CoClustering: Collaborative filtering based on co-clustering[5].
type CoClustering struct {
	Base
	GlobalMean       float64     // A^{global}
	UserMeans        []float64   // A^{R}
	ItemMeans        []float64   // A^{R}
	UserClusters     []int       // p(i)
	ItemClusters     []int       // y(i)
	UserClusterMeans []float64   // A^{RC}
	ItemClusterMeans []float64   // A^{CC}
	CoClusterMeans   [][]float64 // A^{COC}
}

// NewCoClustering creates a co-clustering model. Params:
//   nEpochs       - The number of iteration of the SGD procedure. Default is 20.
//   nUserClusters - The number of user clusters. Default is 3.
//   nItemClusters - The number of item clusters. Default is 3.
//   randState     - The random seed. Default is UNIX time step.
func NewCoClustering(params Params) *CoClustering {
	cc := new(CoClustering)
	cc.Params = params
	return cc
}

// Predict by a co-clustering model.
func (coc *CoClustering) Predict(userId, itemId int) float64 {
	// Convert to inner Id
	innerUserId := coc.UserIdSet.ToDenseId(userId)
	innerItemId := coc.ItemIdSet.ToDenseId(itemId)
	prediction := 0.0
	if innerUserId != NewId && innerItemId != NewId {
		// old user - old item
		userCluster := coc.UserClusters[innerUserId]
		itemCluster := coc.ItemClusters[innerItemId]
		prediction = coc.UserMeans[innerUserId] + coc.ItemMeans[innerItemId] -
			coc.UserClusterMeans[userCluster] - coc.ItemClusterMeans[itemCluster] +
			coc.CoClusterMeans[userCluster][itemCluster]
	} else if innerUserId != NewId {
		// old user - new item
		prediction = coc.UserMeans[innerUserId]
	} else if innerItemId != NewId {
		// new user - old item
		prediction = coc.ItemMeans[innerItemId]
	} else {
		// new user - new item
		prediction = coc.GlobalMean
	}
	return prediction
}

// Fit a co-clustering model.
func (coc *CoClustering) Fit(trainSet TrainSet) {
	coc.Base.Fit(trainSet)
	// Setup parameters
	nUserClusters := coc.Params.GetInt("nUserClusters", 3)
	nItemClusters := coc.Params.GetInt("nItemClusters", 3)
	nEpochs := coc.Params.GetInt("nEpochs", 20)
	// Initialize parameters
	coc.GlobalMean = trainSet.GlobalMean
	userRatings := trainSet.UserRatings()
	itemRatings := trainSet.ItemRatings()
	coc.UserMeans = means(userRatings)
	coc.ItemMeans = means(itemRatings)
	coc.UserClusters = coc.rng.MakeUniformVectorInt(trainSet.UserCount, 0, nUserClusters)
	coc.ItemClusters = coc.rng.MakeUniformVectorInt(trainSet.ItemCount, 0, nItemClusters)
	coc.UserClusterMeans = make([]float64, nUserClusters)
	coc.ItemClusterMeans = make([]float64, nItemClusters)
	coc.CoClusterMeans = newZeroMatrix(nUserClusters, nItemClusters)
	// A^{tmp1}_{ij} = A_{ij} - A^R_i - A^C_j
	tmp1 := newNanMatrix(trainSet.UserCount, trainSet.ItemCount)
	for i := range tmp1 {
		for _, idRating := range userRatings[i] {
			tmp1[i][idRating.Id] = idRating.Rating - coc.UserMeans[i] - coc.ItemMeans[idRating.Id]
		}
	}
	// Clustering
	for ep := 0; ep < nEpochs; ep++ {
		// Compute averages A^{COC}, A^{RC}, A^{CC}, A^R, A^C
		clusterMean(coc.UserClusterMeans, coc.UserClusters, userRatings)
		clusterMean(coc.ItemClusterMeans, coc.ItemClusters, itemRatings)
		coClusterMean(coc.CoClusterMeans, coc.UserClusters, coc.ItemClusters, userRatings)
		// A^{tmp2}_{ih} = \frac {\sum_{j'|y(j')=h}A^{tmp1}_{ij'}} {\sum_{j'|y(j')=h}W_{ij'}} + A^{CC}_h
		tmp2 := newZeroMatrix(trainSet.UserCount, nItemClusters)
		count2 := newZeroMatrix(trainSet.UserCount, nItemClusters)
		for i := range tmp2 {
			for _, ir := range userRatings[i] {
				itemClass := coc.ItemClusters[ir.Id]
				tmp2[i][itemClass] += tmp1[i][ir.Id]
				count2[i][itemClass]++
			}
			for h := range tmp2[i] {
				tmp2[i][h] /= count2[i][h]
				tmp2[i][h] += coc.ItemClusterMeans[h]
			}
		}
		// Update row (user) cluster assignments
		for i := range coc.UserClusters {
			bestCluster, leastCost := coc.UserClusters[i], math.Inf(1)
			for g := 0; g < nUserClusters; g++ {
				// \sum^l_{h=1} A^{tmp2}_{ig} - A^{COC}_{gh} + A^{RC}_g
				cost := 0.0
				for h := 0; h < nItemClusters; h++ {
					if !math.IsNaN(tmp2[i][h]) {
						temp := tmp2[i][h] - coc.CoClusterMeans[g][h] + coc.UserClusterMeans[g]
						cost += temp * temp
					}
				}
				if cost < leastCost {
					bestCluster = g
					leastCost = cost
				}
			}
			coc.UserClusters[i] = bestCluster
		}
		// A^{tmp3}_{gj} = \frac {\sum_{i'|p(i')=g}A^{tmp1}_{i'j}} {\sum_{i'|p(i')=g}W_{i'j}} + A^{RC}_g
		tmp3 := newZeroMatrix(nUserClusters, trainSet.ItemCount)
		count3 := newZeroMatrix(nUserClusters, trainSet.ItemCount)
		for j := range coc.ItemClusters {
			for _, ur := range itemRatings[j] {
				userClass := coc.UserClusters[ur.Id]
				tmp3[userClass][j] += tmp1[ur.Id][j]
				count3[userClass][j]++
			}
			for g := range tmp3 {
				tmp3[g][j] /= count3[g][j]
				tmp3[g][j] += coc.UserClusterMeans[g]
			}
		}
		// Update column (item) cluster assignments
		for j := range coc.ItemClusters {
			bestCluster, leastCost := coc.ItemClusters[j], math.Inf(1)
			for h := 0; h < nItemClusters; h++ {
				// \sum^k_{h=1} A^{tmp3}_{gj} - A^{COC}_{gh} + A^{CC}_h
				cost := 0.0
				for g := 0; g < nUserClusters; g++ {
					if !math.IsNaN(tmp3[g][j]) {
						temp := tmp3[g][j] - coc.CoClusterMeans[g][h] + coc.ItemClusterMeans[h]
						cost += temp * temp
					}
				}
				if cost < leastCost {
					bestCluster = h
					leastCost = cost
				}
			}
			coc.ItemClusters[j] = bestCluster
		}
	}
}

func clusterMean(dst []float64, clusters []int, idRatings [][]IdRating) {
	resetZeroVector(dst)
	count := make([]float64, len(dst))
	for id, cluster := range clusters {
		for _, ir := range idRatings[id] {
			dst[cluster] += ir.Rating
			count[cluster]++
		}
	}
	floats.Div(dst, count)
}

func coClusterMean(dst [][]float64, userClusters, itemClusters []int, userRatings [][]IdRating) {
	resetZeroMatrix(dst)
	count := newZeroMatrix(len(dst), len(dst[0]))
	for userId, userCluster := range userClusters {
		for _, ir := range userRatings[userId] {
			itemCluster := itemClusters[ir.Id]
			count[userCluster][itemCluster]++
			dst[userCluster][itemCluster] += ir.Rating
		}
	}
	for i := range dst {
		for j := range dst[i] {
			dst[i][j] /= count[i][j]
		}
	}
}
