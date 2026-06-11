import pandas as pd
import matplotlib.pyplot as plt

# Przykładowe dane (podmień na swoje logi z quic-go)
data = [
{"offset": 0,    "block_s1": 1200, "block_s2": 900},
{"offset": 1000, "block_s1": 1300, "block_s2": 950},
{"offset": 2000, "block_s1": 1250, "block_s2": 1100},
{"offset": 3000, "block_s1": 1400, "block_s2": 1050},
{"offset": 4000, "block_s1": 1350, "block_s2": 1200},
{"offset": 5000, "block_s1": 1500, "block_s2": 1150},
{"offset": 6000, "block_s1": 1450, "block_s2": 1300},
]

df = pd.DataFrame(data)

# --- wykres ---
plt.figure(figsize=(10, 5))

plt.plot(df["offset"], df["block_s1"], marker="o", label="S1 block size")
plt.plot(df["offset"], df["block_s2"], marker="o", label="S2 block size")

plt.title("QUIC: Block Size vs Offset (S1 vs S2)")
plt.xlabel("Offset (bytes)")
plt.ylabel("Block size (bytes)")

plt.grid(True, linestyle="--", alpha=0.5)
plt.legend()

plt.tight_layout()
plt.show()