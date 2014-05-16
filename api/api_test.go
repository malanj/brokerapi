package api_test

import (
	"errors"
	"fmt"

	"github.com/cloudfoundry/gosteno"
	"github.com/codegangsta/martini"
	"github.com/drewolson/testflight"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/pivotal-cf-experimental/go-service-broker/api"
)

func configureBrokerTestSinkLogger(sink *gosteno.TestingSink) *gosteno.Logger {
	logFlags := gosteno.EXCLUDE_DATA | gosteno.EXCLUDE_FILE | gosteno.EXCLUDE_LINE | gosteno.EXCLUDE_METHOD
	gostenoConfig := &gosteno.Config{
		Sinks:     []gosteno.Sink{sink},
		Level:     gosteno.LOG_INFO,
		Codec:     gosteno.NewJsonPrettifier(logFlags),
		EnableLOC: true,
	}
	gosteno.Init(gostenoConfig)
	return gosteno.NewLogger("brokerLogger")
}

func sinkContains(sink *gosteno.TestingSink, loggingMessage string) bool {
	foundMessage := false
	for _, record := range sink.Records {
		if record.Message == loggingMessage {
			foundMessage = true
			break
		}
	}

	if !foundMessage {
		fmt.Printf("Didn't find [%s]\n", loggingMessage)

		for index, record := range sink.Records {
			fmt.Printf("Index %d: [%s] \n", index, record.Message)
		}
	}

	return foundMessage
}

var _ = Describe("Service Broker API", func() {
	var fakeServiceBroker *FakeServiceBroker
	var brokerAPI *martini.ClassicMartini
	var sink *gosteno.TestingSink

	makeInstanceProvisioningRequest := func(instanceID string) *testflight.Response {
		response := &testflight.Response{}
		testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
			path := fmt.Sprintf("/v2/service_instances/%s", instanceID)
			response = r.Put(path, "application/json", "")
		})
		return response
	}

	BeforeEach(func() {
		fakeServiceBroker = &FakeServiceBroker{
			InstanceLimit: 3,
		}
		sink = gosteno.NewTestingSink()
		brokerLogger := configureBrokerTestSinkLogger(sink)

		brokerAPI = api.New(fakeServiceBroker, nullLogger(), brokerLogger)
	})

	Describe("catalog endpoint", func() {
		makeCatalogRequest := func() *testflight.Response {
			response := &testflight.Response{}
			testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
				response = r.Get("/v2/catalog")
			})
			return response
		}

		It("returns a 200", func() {
			response := makeCatalogRequest()
			Expect(response.StatusCode).To(Equal(200))
		})

		It("returns valid catalog json", func() {
			response := makeCatalogRequest()
			Expect(response.Body).To(MatchJSON(fixture("catalog.json")))
		})
	})

	Describe("instance lifecycle endpoint", func() {
		makeInstanceDeprovisioningRequest := func(instanceID string) *testflight.Response {
			response := &testflight.Response{}
			testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
				path := fmt.Sprintf("/v2/service_instances/%s", instanceID)
				response = r.Delete(path, "application/json", "")
			})
			return response
		}

		Describe("provisioning", func() {
			It("calls Provision on the service broker with the instance id", func() {
				instanceID := uniqueInstanceID()
				makeInstanceProvisioningRequest(instanceID)
				Expect(fakeServiceBroker.ProvisionedInstanceIDs).To(ContainElement(instanceID))
			})

			Context("when the instance does not exist", func() {
				It("returns a 201", func() {
					response := makeInstanceProvisioningRequest(uniqueInstanceID())
					Expect(response.StatusCode).To(Equal(201))
				})

				It("returns json with a dashboard_url field", func() {
					response := makeInstanceProvisioningRequest(uniqueInstanceID())
					Expect(response.Body).To(MatchJSON(fixture("provisioning.json")))
				})

				Context("when the instance limit has been reached", func() {
					BeforeEach(func() {
						for i := 0; i < fakeServiceBroker.InstanceLimit; i++ {
							makeInstanceProvisioningRequest(uniqueInstanceID())
						}
					})

					It("returns a 500", func() {
						response := makeInstanceProvisioningRequest(uniqueInstanceID())
						Expect(response.StatusCode).To(Equal(500))
					})

					It("returns json with a description field and a useful error message", func() {
						response := makeInstanceProvisioningRequest(uniqueInstanceID())
						Expect(response.Body).To(MatchJSON(fixture("instance_limit_error.json")))
					})

					It("logs an appropriate error", func() {
						makeInstanceProvisioningRequest(uniqueInstanceID())
						Expect(sinkContains(sink, "Provisioning error: instance limit for this service has been reached")).To(BeTrue())
					})
				})

				Context("when an unexpected error occurs", func() {
					BeforeEach(func() {
						fakeServiceBroker.ProvisionError = errors.New("broker failed")
					})

					It("returns a 500", func() {
						response := makeInstanceProvisioningRequest(uniqueInstanceID())
						Expect(response.StatusCode).To(Equal(500))
					})

					It("returns json with a description field and a useful error message", func() {
						response := makeInstanceProvisioningRequest(uniqueInstanceID())
						Expect(response.Body).To(MatchJSON(fixture("unexpected_error.json")))
					})

					It("logs an appropriate error", func() {
						makeInstanceProvisioningRequest(uniqueInstanceID())
						Expect(sinkContains(sink, "Provisioning error: broker failed")).To(BeTrue())
					})
				})

			})

			Context("when the instance already exists", func() {
				var instanceID string

				BeforeEach(func() {
					instanceID = uniqueInstanceID()
					makeInstanceProvisioningRequest(instanceID)
				})

				It("returns a 409", func() {
					response := makeInstanceProvisioningRequest(instanceID)
					Expect(response.StatusCode).To(Equal(409))
				})

				It("returns an empty JSON object", func() {
					response := makeInstanceProvisioningRequest(instanceID)
					Expect(response.Body).To(Equal(`{}`))
				})

				It("logs an appropriate error", func() {
					makeInstanceProvisioningRequest(instanceID)
					errorLog := fmt.Sprintf("Provisioning error: instance %s already exists", instanceID)
					Expect(sinkContains(sink, errorLog)).To(BeTrue())
				})
			})
		})

		Describe("deprovisioning", func() {
			It("calls Deprovision on the service broker with the instance id", func() {
				instanceID := uniqueInstanceID()
				makeInstanceDeprovisioningRequest(instanceID)
				Expect(fakeServiceBroker.DeprovisionedInstanceIDs).To(ContainElement(instanceID))
			})

			Context("when the instance exists", func() {
				var instanceID string

				BeforeEach(func() {
					instanceID = uniqueInstanceID()
					makeInstanceProvisioningRequest(instanceID)
				})

				It("returns a 200", func() {
					response := makeInstanceDeprovisioningRequest(instanceID)
					Expect(response.StatusCode).To(Equal(200))
				})

				It("returns an empty JSON object", func() {
					response := makeInstanceDeprovisioningRequest(instanceID)
					Expect(response.Body).To(Equal(`{}`))
				})
			})

			Context("when the instance does not exist", func() {
				var instanceID string

				It("returns a 410", func() {
					response := makeInstanceDeprovisioningRequest(uniqueInstanceID())
					Expect(response.StatusCode).To(Equal(410))
				})

				It("returns an empty JSON object", func() {
					response := makeInstanceDeprovisioningRequest(uniqueInstanceID())
					Expect(response.Body).To(Equal(`{}`))
				})

				It("logs an appropriate error", func() {
					instanceID = uniqueInstanceID()
					makeInstanceDeprovisioningRequest(instanceID)
					errorLog := fmt.Sprintf("Deprovisioning error: instance %s does not exist", instanceID)
					Expect(sinkContains(sink, errorLog)).To(BeTrue())
				})
			})
		})
	})

	Describe("binding lifecycle endpoint", func() {
		makeBindingRequest := func(instanceID string, bindingID string) *testflight.Response {
			response := &testflight.Response{}
			testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
				path := fmt.Sprintf("/v2/service_instances/%s/service_bindings/%s",
					instanceID, bindingID)
				response = r.Put(path, "application/json", "")
			})
			return response
		}

		Describe("binding", func() {

			Context("when the associated instance exists", func() {
				It("calls Bind on the service broker with the instance and binding ids", func() {
					instanceID := uniqueInstanceID()
					bindingID := uniqueBindingID()
					makeBindingRequest(instanceID, bindingID)
					Expect(fakeServiceBroker.BoundInstanceIDs).To(ContainElement(instanceID))
					Expect(fakeServiceBroker.BoundBindingIDs).To(ContainElement(bindingID))
				})

				It("returns the credentials returned by Bind", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.Body).To(MatchJSON(fixture("binding.json")))
				})

				It("returns a 201", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.StatusCode).To(Equal(201))
				})
			})

			Context("when the associated instance does not exist", func() {
				var instanceID string

				BeforeEach(func() {
					fakeServiceBroker.BindError = api.ErrInstanceDoesNotExist
				})

				It("returns a 404", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.StatusCode).To(Equal(404))
				})

				It("returns an error JSON object", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.Body).To(MatchJSON(`{"description":"instance does not exist"}`))
				})

				It("logs an appropriate error", func() {
					instanceID = uniqueInstanceID()
					makeBindingRequest(instanceID, uniqueBindingID())
					errorLog := fmt.Sprintf("Binding error: instance %s does not exist", instanceID)
					Expect(sinkContains(sink, errorLog)).To(BeTrue())
				})
			})

			Context("when the requested binding already exists", func() {
				var instanceID string

				BeforeEach(func() {
					fakeServiceBroker.BindError = api.ErrBindingAlreadyExists
				})

				It("returns a 409", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.StatusCode).To(Equal(409))
				})

				It("returns an error JSON object", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.Body).To(MatchJSON(`{"description":"binding already exists"}`))
				})

				It("logs an appropriate error", func() {
					instanceID = uniqueInstanceID()
					makeBindingRequest(instanceID, uniqueBindingID())
					makeBindingRequest(instanceID, uniqueBindingID())
					errorLog := fmt.Sprintf("Binding error: binding already exists")
					Expect(sinkContains(sink, errorLog)).To(BeTrue())
				})
			})

			Context("when the binding returns an error", func() {
				BeforeEach(func() {
					fakeServiceBroker.BindError = errors.New("everything is kind of wrong")
				})

				It("returns a 500 error response", func() {
					response := makeBindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.StatusCode).To(Equal(500))
					Expect(response.Body).To(MatchJSON(`{"description":"everything is kind of wrong"}`))
				})
			})
		})

		Describe("unbinding", func() {
			makeUnbindingRequest := func(instanceID string, bindingID string) *testflight.Response {
				response := &testflight.Response{}
				testflight.WithServer(brokerAPI, func(r *testflight.Requester) {
					path := fmt.Sprintf("/v2/service_instances/%s/service_bindings/%s",
						instanceID, bindingID)
					response = r.Delete(path, "application/json", "")
				})
				return response
			}

			Context("when the associated instance exists", func() {
				var instanceID string

				BeforeEach(func() {
					instanceID = uniqueInstanceID()
					makeInstanceProvisioningRequest(instanceID)
				})

				Context("and the binding exists", func() {
					var bindingID string

					BeforeEach(func() {
						bindingID = uniqueBindingID()
						makeBindingRequest(instanceID, bindingID)
					})

					It("returns a 200", func() {
						response := makeUnbindingRequest(instanceID, bindingID)
						Expect(response.StatusCode).To(Equal(200))
					})

					It("returns an empty JSON object", func() {
						response := makeUnbindingRequest(instanceID, bindingID)
						Expect(response.Body).To(Equal(`{}`))
					})
				})

				Context("but the binding does not exist", func() {
					It("returns a 410", func() {
						response := makeUnbindingRequest(instanceID, "does-not-exist")
						Expect(response.StatusCode).To(Equal(410))
					})

					It("logs an appropriate error message", func() {
						makeUnbindingRequest(instanceID, "does-not-exist")
						errorLog := fmt.Sprintf("Unbinding error: binding %s does not exist", "does-not-exist")
						Expect(sinkContains(sink, errorLog)).To(BeTrue())
					})
				})
			})

			Context("when the associated instance does not exist", func() {
				var instanceID string

				It("returns a 404", func() {
					response := makeUnbindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.StatusCode).To(Equal(404))
				})

				It("returns an empty JSON object", func() {
					response := makeUnbindingRequest(uniqueInstanceID(), uniqueBindingID())
					Expect(response.Body).To(Equal(`{}`))
				})

				It("logs an appropriate error", func() {
					instanceID = uniqueInstanceID()
					makeUnbindingRequest(instanceID, uniqueBindingID())
					errorLog := fmt.Sprintf("Unbinding error: instance %s does not exist", instanceID)
					Expect(sinkContains(sink, errorLog)).To(BeTrue())
				})
			})
		})
	})
})