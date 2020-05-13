# tests/istio.mk defines test targets for Istio
helm3/test/install:
	kubectl create ns istio-system || true
	# Base install with helm3 works only for a fresh cluster - in many cases
	# we want to upgrade. Helm3 would complain about existing resourceshelm3 install istio-base manifests/charts/charts/base
	helm3 install -n istio-system istio-16 manifests/charts/charts/istio-control/istio-discovery -f manifests/charts/charts/global.yaml
	helm3 install -n istio-system istio-canary manifests/charts/charts/istio-control/istio-discovery -f manifests/charts/charts/global.yaml  \
		--set revision=canary

# Will install or upgrade a 'default' and 'canary' revisions.
# The canary has DNS capture enabled.
helm3/test/upgrade:
	kubectl create ns istio-system || true
	helm3 template istio-base manifests/charts/charts/base | kubectl apply -f -
	helm3 upgrade -i -n istio-system istio-16 manifests/charts/charts/istio-control/istio-discovery \
		--set global.tag=${TAG} --set global.hub=${HUB} \
		-f manifests/charts/charts/global.yaml \
		 --set meshConfig.defaultConfig.proxyMetadata.DNS_CAPTURE="" \
		 --set meshConfig.defaultConfig.proxyMetadata.DNS_AGENT=""


	helm3 upgrade -i -n istio-system istio-canary manifests/charts/charts/istio-control/istio-discovery \
		-f manifests/charts/charts/global.yaml  \
		--set global.tag=${TAG} --set global.hub=${HUB} \
        --set revision=canary \
        --set meshConfig.defaultConfig.proxyMetadata.DNS_CAPTURE=ALL \
        --set meshConfig.defaultConfig.proxyMetadata.DNS_AGENT=DNS-TLS

helm3/test/uninstall:
	helm3 delete -n istio-system istio-16 || true
	helm3 delete -n istio-system istio-canary || true
	helm3 delete istio-base || true
	kubectl delete crd -l release=istio || true
