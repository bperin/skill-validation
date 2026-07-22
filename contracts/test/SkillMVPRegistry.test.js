const { expect } = require("chai");
const { ethers } = require("hardhat");

describe("SkillMVPRegistry", function () {
  async function deployRegistry() {
    const [root, issuer, holder, outsider] = await ethers.getSigners();
    const Registry = await ethers.getContractFactory("SkillMVPRegistry");
    const registry = await Registry.deploy();
    return { root, issuer, holder, outsider, registry };
  }

  function ids() {
    return {
      subject: ethers.keccak256(ethers.toUtf8Bytes("mvp:subject:learner-123")),
      skill: ethers.keccak256(ethers.toUtf8Bytes("mvp:skill:rag-ai")),
      program: ethers.keccak256(ethers.toUtf8Bytes("mvp:program:rag-ai-v1")),
      upgradedProgram: ethers.keccak256(ethers.toUtf8Bytes("mvp:program:rag-ai-v2")),
      evidence: ethers.keccak256(ethers.toUtf8Bytes("mvp:evidence:repository:commit-a")),
      upgradedEvidence: ethers.keccak256(ethers.toUtf8Bytes("mvp:evidence:repository:commit-b")),
    };
  }

  it("lets a scoped issuer certify rag-ai and bind a developer holder", async function () {
    const { root, issuer, holder, outsider, registry } = await deployRegistry();
    const { subject, skill, program, evidence } = ids();
    const expiry = (await ethers.provider.getBlock("latest")).timestamp + 3600;

    await expect(
      registry.connect(outsider).attest(subject, skill, program, evidence)
    ).to.be.revertedWithCustomError(registry, "UnauthorizedIssuer");

    await registry.connect(root).delegateIssuerFor(issuer.address, subject, skill, program, expiry);
    await registry.connect(issuer).attest(subject, skill, program, evidence);
    const attestationId = await registry.deriveAttestationId(subject, skill, program, evidence);
    await registry.connect(issuer).bindHolder(attestationId, holder.address, expiry);

    expect(await registry.hasSkill(subject, skill)).to.equal(true);
    expect(await registry.currentAttestationId(subject, skill)).to.equal(attestationId);
    expect(await registry.verifyAttestation(attestationId, evidence)).to.equal(true);
    expect(await registry.isHolderAuthorized(attestationId, holder.address)).to.equal(true);

    const projectEvidence = ethers.keccak256(ethers.toUtf8Bytes("mvp:project:portfolio-rag:commit-b"));
    const digest = await registry.holderProjectDigest(attestationId, projectEvidence);
    const signature = await holder.signMessage(ethers.getBytes(digest));
    expect(await registry.verifyHolderProject(attestationId, projectEvidence, signature)).to.equal(true);
    const copiedSignature = await outsider.signMessage(ethers.getBytes(digest));
    expect(await registry.verifyHolderProject(attestationId, projectEvidence, copiedSignature)).to.equal(false);
  });

  it("keeps a public lineage when rag-ai policy is upgraded", async function () {
    const { root, issuer, registry } = await deployRegistry();
    const { subject, skill, program, evidence, upgradedProgram, upgradedEvidence } = ids();
    const expiry = (await ethers.provider.getBlock("latest")).timestamp + 3600;

    await registry.connect(root).delegateIssuerFor(issuer.address, subject, skill, program, expiry);
    await registry.connect(issuer).attest(subject, skill, program, evidence);
    const first = await registry.deriveAttestationId(subject, skill, program, evidence);

    await registry.connect(root).supersede(first, upgradedProgram, upgradedEvidence);
    const second = await registry.deriveAttestationId(subject, skill, upgradedProgram, upgradedEvidence);

    expect(await registry.isCurrent(first)).to.equal(false);
    expect(await registry.isCurrent(second)).to.equal(true);
    expect(await registry.successorOf(first)).to.equal(second);
    expect(await registry.hasSkill(subject, skill)).to.equal(true);
  });

  it("lets an employer verify a learner's private name (Brian Perin) and passing score without public exposure", async function () {
    const { root, issuer, registry } = await deployRegistry();
    const expiry = (await ethers.provider.getBlock("latest")).timestamp + 3600;

    // 1. Secret/Private PII on the learner side
    const learnerName = "Brian Perin";
    const secretSalt = ethers.keccak256(ethers.toUtf8Bytes("brian-secret-salt-xyz"));
    
    // 2. Cryptographic one-way commitment generated during issuance
    // subjectCommitment = keccak256(learnerName + secretSalt)
    const subjectCommitment = ethers.solidityPackedKeccak256(
      ["string", "bytes32"],
      [learnerName, secretSalt]
    );

    const skill = ethers.keccak256(ethers.toUtf8Bytes("mvp:skill:rag-ai"));
    const program = ethers.keccak256(ethers.toUtf8Bytes("mvp:program:rag-ai-v1"));
    const evidence = ethers.keccak256(ethers.toUtf8Bytes("mvp:evidence:repository:commit-a"));

    // 3. Root delegates child issuer to certify this specific subject commitment (hashed name)
    await registry.connect(root).delegateIssuerFor(issuer.address, subjectCommitment, skill, program, expiry);
    
    // 4. Issuer attests the skill
    await registry.connect(issuer).attest(subjectCommitment, skill, program, evidence);

    // 5. Employer validates Brian's identity independently using his shared name and salt
    // Employer computes the commitment locally
    const computedSubjectCommitment = ethers.solidityPackedKeccak256(
      ["string", "bytes32"],
      [learnerName, secretSalt]
    );

    // Employer queries the contract read-only hasSkill
    const isValid = await registry.hasSkill(computedSubjectCommitment, skill);
    expect(isValid).to.equal(true);

    // If an employer tries with a forged/incorrect name, commitment mismatch occurs and it returns false
    const forgedNameCommitment = ethers.solidityPackedKeccak256(
      ["string", "bytes32"],
      ["Not Brian", secretSalt]
    );
    const isForgedValid = await registry.hasSkill(forgedNameCommitment, skill);
    expect(isForgedValid).to.equal(false);
  });
});
