"""The bus's transport: a Redis Stream between a producer and the dispatcher.

``wire`` is the contract; ``publisher`` writes it; ``dispatcher`` reads it and
hands each event to the local bus; ``runner`` keeps a dispatcher alive for the
length of a process.
"""
