## How a result reaches the ward

```mermaid
sequenceDiagram
  participant Lab
  participant EPR as Records
  participant Nurse
  participant Doctor
  Lab->>EPR: Culture positive
  EPR-->>Nurse: Alert on the board
  Nurse->>Doctor: Bleep with the result
  Doctor-->>Nurse: Start antibiotics
  Note over Nurse,Doctor: Within 1 hour
```

The alert payload looks like this:

```json
{
  "patient": "bed-4",
  "result": "Gram-negative rods",
  "priority": "urgent"
}
```

Ask IT to add the ward's bleep to the routing table [citation:3301].
