IMAGE:= databus23/impfe
VERSION := 0.1.1

build:
	docker build -t $(IMAGE):$(VERSION) . 

push:
	docker push $(IMAGE):$(VERSION)
