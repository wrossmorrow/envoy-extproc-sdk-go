
Test cases setup. In this directory, run
```
just up -d
```
or if you want to use another terminal and see all the logs
```
just up
```
Then run the usual
```
go test -v
```
or
```
just test
```
The meat of the tests is in `cases.yaml`, a file the code will read for parametrized tests against a running setup with the extproc defined in `tester.go`. 
