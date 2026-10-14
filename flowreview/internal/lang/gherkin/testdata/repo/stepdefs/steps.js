// cucumber-js step definitions, fixture data for gherkin package tests.
const { Given, When } = require('@cucumber/cucumber');

When('they request a greeting', function () {
  this.greeting = 'hi';
});

// Deliberately collides with stepdefs.ambiguousOne in more_steps.go: both
// match "something ambiguous happens".
Given(/^something ambiguous happens$/, function () {
  this.ambiguous = true;
});
