import time

from litellm import atranscription

from app.config import config as app_config
from app.observability.domains import speech
from app.speech.hosted_only import refuse_a_local_speech_service


async def transcribe_audio(file_path: str, filename: str) -> str:
    """Transcribe an upload with the model ``STT_SERVICE`` names.

    There is no on-box transcriber any more, so the two values that used to
    mean one -- ``local/<size>`` and unset, which defaulted to ``local/base``
    -- stop here naming the setting instead of transcribing with something
    nobody chose.
    """
    model = app_config.STT_SERVICE
    refuse_a_local_speech_service("STT_SERVICE", model)
    if not model:
        raise ValueError(
            "STT_SERVICE is not configured, so this upload cannot be "
            "transcribed. Name a model the proxy serves, for example "
            "STT_SERVICE=openai/groq-whisper-large-v3."
        )

    t0 = time.perf_counter()
    with speech.transcription_span(provider="litellm", model=model):
        try:
            with open(file_path, "rb") as audio_file:
                kwargs: dict = {
                    "model": model,
                    "file": audio_file,
                    "api_key": app_config.STT_SERVICE_API_KEY,
                }
                if app_config.STT_SERVICE_API_BASE:
                    kwargs["api_base"] = app_config.STT_SERVICE_API_BASE
                response = await atranscription(**kwargs)
                text = response.get("text", "")
                if not text:
                    raise ValueError("Transcription returned empty text")

            return f"# Transcription of {filename}\n\n{text}"
        finally:
            speech.record_transcription_duration(
                (time.perf_counter() - t0) * 1000, provider="litellm", model=model
            )
