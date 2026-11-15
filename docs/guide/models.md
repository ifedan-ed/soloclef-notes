# Models

Every answer, summary and output is written by an AI model. The server comes
with its own models, and you can add your own. Both are under Settings,
Models.

## The server's models

The server's administrator chooses which models are offered, and everyone on
the server sees the same list. Their cost is the server's, not yours.

## Choosing a model for each job

Model Roles picks a model for each job:

| Role | What it does | Unless you choose |
| --- | --- | --- |
| Chat model | Answers in the chat. | The server's default. |
| Quick tasks model | Small helper calls: Studio suggestions, follow-up questions, source guides and chat titles. | Your chat model. |
| Studio outputs model | Writes every Studio output. | Your chat model. |
| Vision model | Reads pictures, such as those in uploads with Vision on. | The server's, if it has one. |
| Image generation model | Draws images. | The server's, if it has one. |

Each menu lists the server's models and the models of your own connections.
The picker beside the chat's send button changes the same chat model; Server
default there means the server's default.

If a chosen model fails, the job fails and says so. No other model is tried
behind your back.

## Your own models

Under Your own models you can connect any service that speaks OpenAI's API,
with your own key. Its models are then yours to pick, marked as yours, and
only you see them.

1. Choose a preset: GPT (OpenAI), Claude (Anthropic), Gemini (Google AI
   Studio), OpenRouter or Requesty. Each fills in the address. For anything
   else, choose OpenAI-Compatible and enter its address.
2. Paste your API key. Add extra headers if the service needs them.
3. Pick the models to use. They are listed from the service; if that fails,
   type their names.
4. Choose Connect. One of the models is tested first, and a connection that
   fails the test is not saved.

These models are called directly at the address you gave, with your key, so
their cost is on your account with that service. The key and headers are
stored encrypted and never shown again.

An address inside the server's own network, such as an Ollama or LM Studio
machine, only works once the server's administrator allows it.
