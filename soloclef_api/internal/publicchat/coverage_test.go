package publicchat

import "testing"

// TestTheRecordedTableCoversEveryBranch keeps a passing replay from meaning
// less than it looks. Each of these is a branch of a route or of a helper it
// reaches, and each has to be in the recorded table with the answer named --
// so a table regenerated without one of them fails here, not silently.
func TestTheRecordedTableCoversEveryBranch(t *testing.T) {
	table := readTable(t)
	statuses := map[string]int{}
	for _, c := range table.Cases {
		statuses[c.Name] = c.Status
	}
	for name, want := range map[string]int{
		// The link itself, on each route.
		"chat_missing":    404,
		"chat_expired":    410,
		"video_expired":   404,
		"podcast_expired": 404,
		// Hostile tokens: the router's own answer, the driver's, and the
		// ordinary "names nothing".
		"chat_token_a_slash_names_nothing":      404,
		"chat_token_with_a_nul_byte":            500,
		"chat_token_invalid_utf8_names_nothing": 404,
		"chat_token_very_long_names_nothing":    404,
		"chat_token_unicode_names_nothing":      404,
		"podcast_query_string_is_ignored":       404,
		// A column Python cannot use, or cannot even load.
		"chat_data_is_null":                         500,
		"podcast_data_is_a_list":                    500,
		"video_data_holds_a_4301_digit_integer":     500,
		"podcast_data_holds_a_4300_digit_integer":   404,
		"video_metadata_holds_a_4301_digit_integer": 500,
		// The integer in the path, and pydantic's two refusals for one that
		// is too long.
		"podcast_id_is_letters":                   422,
		"podcast_id_is_a_slash":                   404,
		"podcast_id_4301_digits":                  422,
		"podcast_id_is_4301_digits_then_a_letter": 422,
		"podcast_id_is_plus_4301_digits":          422,
		"podcast_id_is_padded_4301_digits":        422,
		"podcast_id_with_many_zeros_in_front":     200,
		"podcast_id_past_int64":                   200,
		// The podcast list, as a Python loop walks it.
		"podcast_found":                             200,
		"podcast_list_null":                         500,
		"podcast_list_an_empty_string":              404,
		"podcast_list_an_object":                    500,
		"podcast_bad_entry_after_the_match":         200,
		"podcast_bad_entry_before_the_match":        500,
		"podcast_id_stored_as_a_float":              200,
		"podcast_id_stored_as_true":                 200,
		"podcast_id_stored_as_text":                 404,
		"podcast_id_wide_against_a_float":           200,
		"podcast_transcript_drops_sa_keys":          200,
		"podcast_a_float_past_float64":              500,
		"podcast_id_against_the_float_it_rounds_to": 404,
		// The video's allowlist, in Python's `in`.
		"video_ready":                                     200,
		"video_id_not_in_the_snapshot":                    404,
		"video_artifact_ids_holds_a_float":                200,
		"video_artifact_ids_holds_true_and_the_id_is_one": 200,
		"video_artifact_ids_is_a_string":                  500,
		"video_artifact_ids_is_an_object":                 404,
		"video_id_past_int32_and_shared":                  500,
		"video_id_past_int64_and_shared":                  500,
		"video_id_past_int64_and_not_shared":              404,
		"video_not_a_video":                               404,
		"video_no_document":                               200,
		"video_metadata_a_list":                           500,
		"video_metadata_slides_missing":                   404,
		// dict() of a slide that is not an object.
		"video_slides_key_and_audio_file_together":          200,
		"video_slides_number_true":                          200,
		"video_slides_entry_null":                           500,
		"video_slides_entry_an_empty_string":                200,
		"video_slides_entry_pairs":                          200,
		"video_slides_entry_two_character_strings":          200,
		"video_slides_entry_two_key_objects":                200,
		"video_slides_entry_keys_that_are_not_text":         200,
		"video_slides_entry_first_key_decides_the_spelling": 200,
		"video_slides_entry_a_key_that_is_a_list":           500,
		"video_slides_entry_pairs_with_an_infinite_key":     500,
		"video_slides_sa_prefixed_keys":                     200,
		"video_slides_a_float_past_float64":                 500,
		// The page of a chat: the response model.
		"chat_ready":                                        200,
		"chat_expires_with_microseconds":                    200,
		"chat_title_null":                                   500,
		"chat_no_title_key":                                 200,
		"chat_snapshot_at_missing":                          500,
		"chat_snapshot_at_plus_0530":                        200,
		"chat_snapshot_at_naive":                            200,
		"chat_snapshot_at_a_whole_number":                   200,
		"chat_snapshot_at_true":                             500,
		"chat_messages_null":                                500,
		"chat_messages_an_empty_object":                     200,
		"chat_messages_a_string":                            500,
		"chat_message_role_uppercase":                       500,
		"chat_message_content_missing":                      500,
		"chat_message_content_null":                         200,
		"chat_message_content_a_float_past_float64":         200,
		"chat_message_content_sa_keys":                      200,
		"chat_message_author_name_null":                     200,
		"chat_message_author_a_string":                      500,
		"chat_message_author_legacy_with_id_and_avatar":     200,
		"chat_message_content_internal_keys_at_every_depth": 200,
		// The legacy upcast, and the set its ids are kept in.
		"chat_upcast_fills_a_missing_format":                              200,
		"chat_upcast_replaces_an_empty_format_in_place":                   200,
		"chat_upcast_a_float_id_rides_on_another_parts_query":             200,
		"chat_upcast_a_list_id_rides_and_fails":                           500,
		"chat_upcast_artifact_ids_holds_a_list_and_there_are_no_messages": 500,
		"chat_upcast_id_past_int32_shared":                                500,
		"chat_upcast_id_true_shared_as_one":                               500,
		"chat_upcast_id_true_first_beside_an_integer":                     500,
		"chat_upcast_id_true_behind_an_integer_in_slot_zero":              200,
		"chat_upcast_id_false_behind_an_integer_in_slot_zero":             200,
		"chat_upcast_id_true_after_the_table_has_grown":                   200,
		// The page of a Studio output.
		"output_ready":                                 200,
		"output_null":                                  200,
		"output_a_list":                                500,
		"output_id_not_shared":                         200,
		"output_artifact_gone":                         200,
		"output_two_primaries":                         200,
		"output_id_text":                               200,
		"output_id_a_fraction":                         500,
		"output_id_a_float_at_two_to_the_63":           500,
		"output_id_a_float_just_under_two_to_the_63":   200,
		"output_id_true_shared_as_one":                 500,
		"output_id_true_not_shared":                    200,
		"output_id_past_int32_shared":                  500,
		"output_artifact_ids_a_string":                 500,
		"output_file_display_unknown":                  500,
		"output_file_size_true":                        200,
		"output_token_is_quoted_into_each_content_url": 200,
		"output_expired_is_410":                        410,
		// Recorded for whoever ports the route this package leaves behind.
		"presentation_number_is_a_list": 200,
	} {
		got, recorded := statuses[name]
		switch {
		case !recorded:
			t.Errorf("the recorded table has no case called %q", name)
		case got != want:
			t.Errorf("%s: Python answered %d and this test expected %d", name, got, want)
		}
	}
}
