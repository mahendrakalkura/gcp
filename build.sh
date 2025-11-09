goimports -w .
go mod tidy || (go get . && go mod tidy)
go build -o ./main
