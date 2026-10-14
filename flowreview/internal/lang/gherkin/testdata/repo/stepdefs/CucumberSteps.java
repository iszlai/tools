package stepdefs;

import io.cucumber.java.en.Given;
import io.cucumber.java.en.Then;

// cucumber-jvm step definitions, fixture data for gherkin package tests.
public class CucumberSteps {

    @Given("there are {int} cucumbers in the basket")
    public void thereAreCucumbersInTheBasket(int count) {
        // no-op fixture
    }

    @Then("the basket has {string}")
    public void theBasketHas(String item) {
        // no-op fixture
    }
}
