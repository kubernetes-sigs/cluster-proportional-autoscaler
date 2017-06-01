# Copyright 2016 The Kubernetes Authors. All rights reserved#
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License..

# Procedure for change:
# 1. Bump the tag number.
# 2. Create an image for your GCR private bucket or Docker Hub bucket and push there: make build push
# 3. Temporarily adjust any config files etc. that reference gcr.io/google_containers/cluster-proportional-autoscaler:$(TAG)
#    to use your chosen private bucket instead.
# 4. Test etc.
# 5. Issue PR.
# 6. When PR is approved make the gcr.io version of the image: make build push
# 7. Revert the references to kubernetes/addon-resizer:$(TAG) to gcr.io/google_containers/cluster-proportional-autoscaler:$(TAG) 

OUT_DIR = build
IMAGE = gcr.io/google_containers/cluster-proportional-autoscaler
TAG = 0.6

# Rules for building the real image for deployment to gcr.io

compile: nanny/
		GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -a -o $(OUT_DIR)/cluster-proportional-autoscaler nanny/main/cluster_proportional_autoscaler.go

go: compile

clean:
	rm -rf build

build: go
	docker build -t ${IMAGE}:$(TAG) .

docker: build

push: docker
	gcloud docker push ${IMAGE}:$(TAG)
