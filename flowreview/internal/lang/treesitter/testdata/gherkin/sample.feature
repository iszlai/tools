Feature: Greeting

  Background:
    Given the system is ready

  Scenario: Say hello
    Given a user named "Ann"
    When they request a greeting
    Then they see "hi Ann"

  Scenario Outline: Say hello to several users
    Given a user named "<name>"
    Then they see "hi <name>"

    Examples:
      | name |
      | Bob  |
