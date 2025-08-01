package relationships

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/meshery/meshery/mesheryctl/pkg/utils"
)

func TestView(t *testing.T) {
	// setup current context
	utils.SetupContextEnv(t)

	//initialize mock server for handling requests
	utils.StartMockery(t)

	// create a test helper
	testContext := utils.NewTestHelper(t)

	// get current directory
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Not able to get current working directory")
	}
	currDir := filepath.Dir(filename)
	fixturesDir := filepath.Join(currDir, "fixtures")

	// test scenarios for fetching data
	tests := []struct {
		Name             string
		Args             []string
		URL              string
		Fixture          string
		Token            string
		ExpectedResponse string
		ExpectError      bool
	}{
		{
			Name:             "View relationship without model name",
			Args:             []string{"view"},
			URL:              testContext.BaseURL + "/api/meshmodels/models/kubernetes/relationships?pagesize=all",
			Fixture:          "",
			ExpectedResponse: "view.relationship.no.arguments.golden",
			Token:            filepath.Join(fixturesDir, "token.golden"),
			ExpectError:      true,
		},
		{
			Name:             "View registered relationship",
			Args:             []string{"view", "kubernetes"},
			URL:              testContext.BaseURL + "/api/meshmodels/models/kubernetes/relationships?pagesize=all",
			Fixture:          "view.relationship.api.response.golden",
			ExpectedResponse: "view.relationship.output.golden",
			Token:            filepath.Join(fixturesDir, "token.golden"),
			ExpectError:      false,
		},
	}

	// run tests
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			if tt.Fixture != "" {
				apiResponse := utils.NewGoldenFile(t, tt.Fixture, fixturesDir).Load()

				utils.TokenFlag = tt.Token

				httpmock.RegisterResponder("GET", tt.URL,
					httpmock.NewStringResponder(200, apiResponse))
			}

			testdataDir := filepath.Join(currDir, "testdata")
			golden := utils.NewGoldenFile(t, tt.ExpectedResponse, testdataDir)

			// Grab console prints with proper cleanup
			originalStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Ensure stdout is always restored
			defer func() {
				os.Stdout = originalStdout
			}()

			_ = utils.SetupMeshkitLoggerTesting(t, false)
			RelationshipCmd.SetArgs(tt.Args)
			RelationshipCmd.SetOut(originalStdout)
			err := RelationshipCmd.Execute()

			// Close write end before reading
			w.Close()

			if err != nil {
				// if we're supposed to get an error
				if tt.ExpectError {
					// write it in file
					if *update {
						golden.Write(err.Error())
					}
					expectedResponse := golden.Load()

					utils.Equals(t, expectedResponse, err.Error())
					return
				}
				t.Fatal(err)
			}

			out, _ := io.ReadAll(r)
			actualResponse := string(out)

			if *update {
				golden.Write(actualResponse)
			}

			expectedResponse := golden.Load()

			cleanedActualResponse := utils.CleanStringFromHandlePagination(actualResponse)
			cleanedExceptedResponse := utils.CleanStringFromHandlePagination(expectedResponse)

			utils.Equals(t, cleanedExceptedResponse, cleanedActualResponse)
		})
		t.Log("View experimental relationship test passed")
	}

	utils.StopMockery(t)
}
