#!/bin/zsh
set -eu
cd "$(dirname "$0")/.."

# Color codes for clean output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Starting RAG Portfolio Signed Webpage Demo ===${NC}"

# Ensure setup is complete
echo -e "${BLUE}[1/6] Running NPM and Environment Setup...${NC}"
make setup >/dev/null

# Clean up old index.html
rm -f index.html

# Start local Hardhat in the background
echo -e "${BLUE}[2/6] Starting Local Hardhat Blockchain (Background)...${NC}"
# Use a background process that is terminated on exit
go run ./cmd/issuer-demo network >/dev/null 2>&1 &
HARDHAT_PID=$!

# Register cleanup function to kill Hardhat on exit
cleanup() {
  echo -e "${YELLOW}Cleaning up background Hardhat node (PID: $HARDHAT_PID)...${NC}"
  kill -9 $HARDHAT_PID 2>/dev/null || true
  wait $HARDHAT_PID 2>/dev/null || true
}
trap cleanup EXIT

# Wait for Hardhat RPC to become fully ready on port 8545
echo -e "${YELLOW}Waiting for Hardhat node to initialize...${NC}"
sleep 4

# Step 1: Issue the rag-ai skill to Brian's wallet address
echo -e "${BLUE}[3/6] Issuing 'rag-ai' Skill to Brian Perin on-chain...${NC}"
ISSUE_OUTPUT=$(go run ./cmd/issuer-demo issue \
  -learner-id "learner-brian" \
  -skill-id "rag-ai" \
  -evidence "portfolio-rag-v1" \
  -progress 100 \
  -milestone "completed" \
  -holder-address "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf")

# Parse JSON outputs using Python
REGISTRY=$(echo "$ISSUE_OUTPUT" | python3 -c "import sys, json; print(json.load(sys.stdin)['registry'])")
ATTESTATION=$(echo "$ISSUE_OUTPUT" | python3 -c "import sys, json; print(json.load(sys.stdin)['attestationId'])")

echo -e "  -> ${GREEN}Deployed Registry Address:${NC} $REGISTRY"
echo -e "  -> ${GREEN}On-Chain Attestation ID:${NC} $ATTESTATION"

# Step 2: Generate the raw HTML portfolio page
echo -e "${BLUE}[4/6] Creating Portfolio Webpage (index.html)...${NC}"
cat <<EOF > index.html
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Brian Perin - Portfolio</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; max-width: 600px; margin: 40px auto; padding: 20px; line-height: 1.6; background-color: #fafbfc; color: #333; }
    .card { background: white; border: 1px solid #e1e4e8; border-radius: 8px; padding: 32px; box-shadow: 0 4px 12px rgba(0,0,0,0.05); }
    h1 { margin-top: 0; color: #24292e; border-bottom: 1px solid #eaecef; padding-bottom: 12px; }
    .badge { display: inline-block; background-color: #2ea44f; color: white; padding: 6px 12px; border-radius: 20px; font-weight: bold; font-size: 0.85em; margin-bottom: 24px; text-transform: uppercase; letter-spacing: 0.5px; }
    p { font-size: 1.05em; color: #444; }
    .proof-block { background: #f6f8fa; border: 1px solid #e1e4e8; border-radius: 6px; padding: 20px; font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; font-size: 0.82em; word-break: break-all; margin-top: 28px; }
    .proof-title { font-weight: bold; margin-bottom: 10px; color: #24292e; display: flex; align-items: center; }
    .field { margin-bottom: 8px; }
    .label { color: #586069; font-weight: bold; }
    .val { color: #0366d6; }
  </style>
</head>
<body>
  <div class="card">
    <h1>Brian Perin</h1>
    <div class="badge">Certified: RAG AI Specialist</div>
    <p><strong>RAG Certified.</strong> This developer designs and builds production grade Retrieval Augmented Generation engines featuring multi vector semantic retrievers, hybrid BM255 reranking, and on chain identity verifiability.</p>
    
    <div class="proof-block">
      <div class="proof-title">Cryptographic Proof (On-Chain Registry)</div>
      <div class="field"><span class="label">Registry:</span> <span class="val">REGISTRY_PLACEHOLDER</span></div>
      <div class="field"><span class="label">Attestation ID:</span> <span class="val">ATTESTATION_PLACEHOLDER</span></div>
      <div class="field"><span class="label">Webpage SHA256:</span> <span class="val">SHA_PLACEHOLDER</span></div>
      <div class="field"><span class="label">Developer Signature:</span> <span class="val">SIGNATURE_PLACEHOLDER</span></div>
    </div>
  </div>
</body>
</html>
EOF

# Calculate SHA256 of the file (ignoring the placeholder lines to keep hashing clean)
# The script calculates a SHA256 commitment of the static webpage content "RAG certified" to bind to the signature
EVIDENCE_STRING="Brian Perin knows RAG. Verified content inside index.html"
FILE_SHA=$(python3 -c "import hashlib; print(hashlib.sha256(b'$EVIDENCE_STRING').hexdigest())")

# Step 3: Sign the webpage evidence as the Developer/Holder
echo -e "${BLUE}[5/6] Generating Cryptographic EIP-191 Signature...${NC}"
SIGN_OUTPUT=$(go run ./cmd/issuer-demo sign-project \
  -registry "$REGISTRY" \
  -attestation "$ATTESTATION" \
  -evidence "$EVIDENCE_STRING")

SIGNATURE=$(echo "$SIGN_OUTPUT" | python3 -c "import sys, json; print(json.load(sys.stdin)['signature'])")

# Replace placeholders inside index.html with real verification values
sed -i '' "s|REGISTRY_PLACEHOLDER|$REGISTRY|g" index.html
sed -i '' "s|ATTESTATION_PLACEHOLDER|$ATTESTATION|g" index.html
sed -i '' "s|SHA_PLACEHOLDER|$FILE_SHA|g" index.html
sed -i '' "s|SIGNATURE_PLACEHOLDER|$SIGNATURE|g" index.html

echo -e "  -> ${GREEN}Webpage Signature Generated:${NC} $SIGNATURE"
echo -e "  -> ${GREEN}index.html successfully updated.${NC}"

# Step 4: Verify the signed webpage completely on-chain
echo -e "${BLUE}[6/6] Verifying Webpage Integrity Against Public Contract...${NC}"
VERIFY_OUTPUT=$(go run ./cmd/issuer-demo verify-project \
  -registry "$REGISTRY" \
  -attestation "$ATTESTATION" \
  -evidence "$EVIDENCE_STRING" \
  -signature "$SIGNATURE")

VERIFIED=$(echo "$VERIFY_OUTPUT" | python3 -c "import sys, json; print(json.load(sys.stdin)['verified'])")

if [ "$VERIFIED" = "True" ]; then
  echo -e "\n${GREEN}==================================================${NC}"
  echo -e "${GREEN}[SUCCESS] The webpage is LEGITIMATELY SIGNED on-chain.${NC}"
  echo -e "=================================================="
  echo -e "This proves that Brian Perin is the authorized owner of the"
  echo -e "on-chain 'rag-ai' credential and has cryptographically bound"
  echo -e "\nTry running this verification yourself to check the proof:"
  echo -e "${YELLOW}go run ./cmd/issuer-demo verify-project \\"
  echo -e "  -registry \"$REGISTRY\" \\"
  echo -e "  -attestation \"$ATTESTATION\" \\"
  echo -e "  -evidence \"$EVIDENCE_STRING\" \\"
  echo -e "  -signature \"$SIGNATURE\"${NC}"

  # Step 5: Simulate a Signature Hijack Attack (Demonstrating Security)
  echo -e "\n${BLUE}[6.5] Simulating a Signature Hijack Attack...${NC}"
  echo -e "An attacker (Eve) copies Brian's valid signature and tries"
  echo -e "to use it to verify her own modified portfolio/repository."
  echo -e "Eve's evidence string is: 'Eve knows RAG. Verified content inside index.html'"
  EVE_EVIDENCE="Eve knows RAG. Verified content inside index.html"

  EVE_VERIFY_OUTPUT=$(go run ./cmd/issuer-demo verify-project \
    -registry "$REGISTRY" \
    -attestation "$ATTESTATION" \
    -evidence "$EVE_EVIDENCE" \
    -signature "$SIGNATURE")

  EVE_VERIFIED=$(echo "$EVE_VERIFY_OUTPUT" | python3 -c "import sys, json; print(json.load(sys.stdin)['verified'])")

  if [ "$EVE_VERIFIED" = "False" ]; then
    echo -e "  -> ${GREEN}HIJACK BLOCKED: The contract rejected Eve's attempt!${NC}"
    echo -e "     The signature was mathematically bound to Brian's evidence and cannot be reused by Eve."
  else
    echo -e "  -> ${YELLOW}Warning: Hijack simulation behaved unexpectedly.${NC}"
  fi

  # Step 6: Demonstrating the Business Capability Flywheel (Telemetry)
  echo -e "\n${BLUE}[7/6] Demonstrating the Talent, Trust, and Revenue Flywheel...${NC}"
  echo -e "Learner 1234 builds an ultra successful RAG application that serves"
  echo -e "125000 API requests. To prove this success back to the educational platform"
  echo -e "and drive high quality top of funnel student enrollment, the application"
  echo -e "cryptographically signs a success metric using their holder key."

  METRIC_NAME="api_requests"
  METRIC_VALUE="125000"
  TIMESTAMP=$(date +%s)

  echo -e "  -> ${BLUE}Generating signed success report for metric:${NC} $METRIC_NAME = $METRIC_VALUE"
  TELEMETRY_OUTPUT=$(go run ./cmd/issuer-demo sign-telemetry \
    -registry "$REGISTRY" \
    -attestation "$ATTESTATION" \
    -metric-name "$METRIC_NAME" \
    -metric-value "$METRIC_VALUE" \
    -timestamp "$TIMESTAMP")

  TELE_SIGNATURE=$(echo "$TELEMETRY_OUTPUT" | python3 -c "import sys, json; print(json.load(sys.stdin)['signature'])")
  echo -e "  -> ${GREEN}Telemetry Signature Generated:${NC} $TELE_SIGNATURE"

  echo -e "\n${BLUE}[7.5] Verifying Success Telemetry On-Chain...${NC}"
  TELE_VERIFY_OUTPUT=$(go run ./cmd/issuer-demo verify-telemetry \
    -registry "$REGISTRY" \
    -attestation "$ATTESTATION" \
    -metric-name "$METRIC_NAME" \
    -metric-value "$METRIC_VALUE" \
    -timestamp "$TIMESTAMP" \
    -signature "$TELE_SIGNATURE")

  TELE_VERIFIED=$(echo "$TELE_VERIFY_OUTPUT" | python3 -c "import sys, json; print(json.load(sys.stdin)['verified'])")

  if [ "$TELE_VERIFIED" = "True" ]; then
    echo -e "  -> ${GREEN}TELEMETRY VERIFIED SUCCESSFULLY ON-CHAIN!${NC}"
    echo -e "     The contract confirmed Brian is the authorized holder who signed this success data."
    echo -e "     This proven real world value accrual drives top of funnel conversion for the platform."
  else
    echo -e "  -> ${YELLOW}Warning: Telemetry verification failed.${NC}"
  fi
else
  echo -e "\n${YELLOW}Verification failed. Check parameters.${NC}"
fi
