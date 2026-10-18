from app.notebooks.studio import output_view
from app.notebooks.studio.output_view import OutputViewRead, output_prompt


def test_prompt_is_the_trimmed_focus():
    assert output_prompt({"focus": "  murmurs \n"}) == "murmurs"
    assert output_prompt({"focus": 3}) == ""
    assert output_prompt({}) == ""
    assert output_prompt(None) == ""


def test_the_retired_rating_is_not_part_of_the_view():
    assert "feedback" not in OutputViewRead.model_fields
    for gone in ("set_feedback", "with_feedback", "stored_feedback", "output_feedback"):
        assert not hasattr(output_view, gone), gone
