Feature: Greeting

  Scenario: Say hello
    Given a user named "Ann" exists
    Then they see "hi Ann"

  Scenario: Request greeting
    When they request a greeting

  Scenario: Cucumber expressions
    Given there are 3 cucumbers in the basket
    Then the basket has "cucumbers"

  Scenario: Unmatched step
    Given nobody implements this step

  Scenario: Ambiguous step
    Given something ambiguous happens

  Scenario Outline: Say hello to several users
    Given a user named "<name>" exists
    Then they see "<greeting>"

    Examples:
      | name | greeting |
      | Bob  | hi Bob   |
