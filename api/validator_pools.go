package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var validatorPools =
	[]core.ValidatorPool{}

func HandleValidatorPools(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	json.NewEncoder(w).Encode(
		validatorPools,
	)
}
func HandleCreatePool(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method !=
		http.MethodPost {

		http.Error(
			w,
			"POST only",
			http.StatusMethodNotAllowed,
		)

		return
	}

	var pool core.ValidatorPool

	err :=
		json.NewDecoder(
			r.Body,
		).Decode(
			&pool,
		)

	if err != nil {

		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)

		return
	}

	validatorPools =
		append(
			validatorPools,
			pool,
		)

	json.NewEncoder(w).Encode(
		map[string]string{
			"status":
				"pool created",
		},
	)
}
func HandleAddPoolMember(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	type Request struct {
		ValidatorID string

		Member core.PoolMember
	}

	var req Request

	err :=
		json.NewDecoder(
			r.Body,
		).Decode(
			&req,
		)

	if err != nil {

		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)

		return
	}

	for i :=
		range validatorPools {

		if validatorPools[i].
			ValidatorID ==
			req.ValidatorID {

			core.AddMember(
				&validatorPools[i],
				req.Member,
			)

			json.NewEncoder(w).Encode(
				map[string]string{
					"status":
						"member added",
				},
			)

			return
		}
	}

	http.Error(
		w,
		"pool not found",
		http.StatusNotFound,
	)
}