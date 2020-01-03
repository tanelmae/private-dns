#!/usr/bin/env bash

export DIR="$(cd $(dirname $(dirname ${BASH_SOURCE[0]})) > /dev/null 2>&1 && pwd)"

GENERATOR="${DIR}/vendor/k8s.io/code-generator/generate-groups.sh"
echo "Working directory: ${DIR}"

export TMPGOATH="${DIR}/gotmp"

# Setup temporary GOPATH to generate the initial files
# Otherwise there is cyclic dependency between generated files and vendorings
if [ ! -f ${GENERATOR} ]; then
	echo "${GENERATOR} not found"
	echo "Will bootstrap the setup"
	GOPATH="${TMPGOATH}"
	mkdir -p ${TMPGOATH}
	go get k8s.io/code-generator@v0.17.0
	GENERATOR="${GOPATH}/pkg/mod/k8s.io/code-generator@v0.17.0/generate-groups.sh"
fi

chmod +x ${GENERATOR}

export CLIENTSET_NAME_VERSIONED="privatedns"
export CUSTOM_RESOURCE_VERSION="v1"

rm -fR "${DIR}/pkg/gen"
"${GENERATOR}" client,informer \
	"github.com/tanelmae/private-dns/pkg/gen" "github.com/tanelmae/private-dns/pkg/apis" \
	privatedns:v1 \
	--go-header-file "${DIR}/codegen/license.go.txt" \
	--output-base ${DIR} --plural-exceptions PrivateDNS:PrivateDNS

"${GENERATOR}" deepcopy,lister \
	"github.com/tanelmae/private-dns/pkg/gen" "github.com/tanelmae/private-dns/pkg/apis" \
	privatedns:v1 \
	--go-header-file "${DIR}/codegen/license.go.txt" \
	--output-base ${DIR}

# Need this hack as code-generator is not able to put them into correct path when using modules
mv "${DIR}/github.com/tanelmae/private-dns/pkg/gen" "${DIR}/pkg/gen"
mv "${DIR}/github.com/tanelmae/private-dns/pkg/apis/privatedns/v1/zz_generated.deepcopy.go" \
	"pkg/apis/privatedns/v1/zz_generated.deepcopy.go"
rm -Rf "${DIR}/github.com"

if [ -d ${TMPGOATH} ]; then
	chmod -R +w ${TMPGOATH}
	rm -Rf ${TMPGOATH}
	echo "Resolve required dependencies:"
	echo "    go mod vendor"
fi
