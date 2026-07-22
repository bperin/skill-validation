const { expect } = require("chai");
const { ethers } = require("hardhat");

describe("SkillRegistry development integration", function () {
  async function deployRegistry() {
    const [root, githubChild, webChild, holder, outsider] = await ethers.getSigners();
    const Verifier = await ethers.getContractFactory("DevelopmentScoreVerifier");
    const verifier = await Verifier.deploy();
    const Registry = await ethers.getContractFactory("SkillRegistry");
    const registry = await Registry.deploy(await verifier.getAddress());
    return { root, githubChild, webChild, holder, outsider, registry };
  }

  it("permits only a scoped child to issue a GitHub deployment attestation", async function () {
    const { root, githubChild, webChild, registry } = await deployRegistry();
    await registry.connect(root).delegateIssuer(githubChild.address, 1);
    await registry.connect(root).delegateIssuer(webChild.address, 2);

    const subject = ethers.keccak256(ethers.toUtf8Bytes("subject-secret-commitment"));
    const credential = ethers.toBeHex(96, 32);
    const program = ethers.keccak256(ethers.toUtf8Bytes("company-github"));
    const evidence = ethers.keccak256(ethers.toUtf8Bytes("repo-url:commit:workflow:artifact-digest"));
    const skill = ethers.keccak256(ethers.toUtf8Bytes("github-deployments"));
    const proof = Array(8).fill(0);
    const input = [96, 80];

    await expect(
      registry.connect(webChild).attestWithProof(subject, credential, evidence, program, 0, [skill], proof, input)
    ).to.be.revertedWithCustomError(registry, "UnauthorizedIssuer");

    await registry.connect(githubChild).attestWithProof(subject, credential, evidence, program, 0, [skill], proof, input);
    expect(await registry.hasSkill(subject, skill)).to.equal(true);
    expect(await registry.verifyAttestation(await registry.deriveAttestationId(subject, credential), evidence)).to.equal(true);
  });

  it("removes issuance authority when the root revokes a child key", async function () {
    const { root, githubChild, registry } = await deployRegistry();
    await registry.connect(root).delegateIssuer(githubChild.address, 1);
    await registry.connect(root).revokeIssuer(githubChild.address);
    const zero = ethers.ZeroHash;
    await expect(
      registry.connect(githubChild).attestWithProof(zero, zero, zero, zero, 0, [zero], Array(8).fill(0), [0, 0])
    ).to.be.revertedWithCustomError(registry, "UnauthorizedIssuer");
  });

  it("enforces a child delegation for one subject, skill, policy, and expiry", async function () {
    const { root, githubChild, registry } = await deployRegistry();
    const subject = ethers.keccak256(ethers.toUtf8Bytes("learner-123"));
    const otherSubject = ethers.keccak256(ethers.toUtf8Bytes("learner-456"));
    const credential = ethers.toBeHex(96, 32);
    const program = ethers.keccak256(ethers.toUtf8Bytes("postgres-v2"));
    const evidence = ethers.keccak256(ethers.toUtf8Bytes("repo:commit:build"));
    const skill = ethers.keccak256(ethers.toUtf8Bytes("postgres"));
    const otherSkill = ethers.keccak256(ethers.toUtf8Bytes("redis"));
    const proof = Array(8).fill(0);
    const input = [96, 80];
    const expiry = (await ethers.provider.getBlock("latest")).timestamp + 3600;

    await registry.connect(root).delegateIssuerFor(githubChild.address, 1, subject, skill, program, expiry);

    await expect(
      registry.connect(githubChild).attestWithProof(otherSubject, credential, evidence, program, 0, [skill], proof, input)
    ).to.be.revertedWithCustomError(registry, "DelegationSubjectMismatch");

    await expect(
      registry.connect(githubChild).attestWithProof(subject, credential, evidence, program, 0, [otherSkill], proof, input)
    ).to.be.revertedWithCustomError(registry, "DelegationSkillMismatch");

    await registry.connect(githubChild).attestWithProof(subject, credential, evidence, program, 0, [skill], proof, input);
    expect(await registry.hasSkill(subject, skill)).to.equal(true);
  });

  it("refuses a scoped child after its delegation expires", async function () {
    const { root, githubChild, registry } = await deployRegistry();
    const subject = ethers.keccak256(ethers.toUtf8Bytes("learner-expired"));
    const credential = ethers.toBeHex(96, 32);
    const program = ethers.keccak256(ethers.toUtf8Bytes("postgres-v2"));
    const evidence = ethers.keccak256(ethers.toUtf8Bytes("repo:commit:build"));
    const skill = ethers.keccak256(ethers.toUtf8Bytes("postgres"));
    const proof = Array(8).fill(0);
    const input = [96, 80];
    const expiry = (await ethers.provider.getBlock("latest")).timestamp + 3600;

    await registry.connect(root).delegateIssuerFor(githubChild.address, 1, subject, skill, program, expiry);
    await ethers.provider.send("evm_setNextBlockTimestamp", [expiry]);
    await ethers.provider.send("evm_mine");

    await expect(
      registry.connect(githubChild).attestWithProof(subject, credential, evidence, program, 0, [skill], proof, input)
    ).to.be.revertedWithCustomError(registry, "DelegationExpired");
  });

  it("lets the root supersede a current skill while retaining its lineage", async function () {
    const { root, githubChild, registry } = await deployRegistry();
    await registry.connect(root).delegateIssuer(githubChild.address, 1);

    const subject = ethers.keccak256(ethers.toUtf8Bytes("subject-commitment"));
    const oldCredential = ethers.toBeHex(96, 32);
    const newCredential = ethers.toBeHex(97, 32);
    const oldEvidence = ethers.keccak256(ethers.toUtf8Bytes("commit-a:run-1:artifact-a"));
    const newEvidence = ethers.keccak256(ethers.toUtf8Bytes("commit-b:run-2:artifact-b"));
    const oldProgram = ethers.keccak256(ethers.toUtf8Bytes("postgres-v1"));
    const newProgram = ethers.keccak256(ethers.toUtf8Bytes("postgres-v2"));
    const skill = ethers.keccak256(ethers.toUtf8Bytes("postgres"));
    const proof = Array(8).fill(0);

    await registry.connect(githubChild).attestWithProof(
      subject, oldCredential, oldEvidence, oldProgram, 0, [skill], proof, [96, 80]
    );
    const previous = await registry.deriveAttestationId(subject, oldCredential);

    await registry.connect(root).supersedeWithProof(
      previous, newCredential, newEvidence, newProgram, proof, [97, 80]
    );

    const successor = await registry.deriveAttestationId(subject, newCredential);
    expect(await registry.successorOf(previous)).to.equal(successor);
    expect(await registry.predecessorOf(successor)).to.equal(previous);
    expect(await registry.isCurrent(previous)).to.equal(false);
    expect(await registry.isCurrent(successor)).to.equal(true);
    expect(await registry.hasSkill(subject, skill)).to.equal(true);
    expect(await registry.verifyAttestation(successor, newEvidence)).to.equal(true);
  });

  it("binds a developer holder key to one active credential without granting issuance", async function () {
    const { root, githubChild, holder, outsider, registry } = await deployRegistry();
    await registry.connect(root).delegateIssuer(githubChild.address, 1);

    const subject = ethers.keccak256(ethers.toUtf8Bytes("holder-subject"));
    const credential = ethers.toBeHex(111, 32);
    const evidence = ethers.keccak256(ethers.toUtf8Bytes("holder-repo:commit"));
    const program = ethers.keccak256(ethers.toUtf8Bytes("postgres-v2"));
    const skill = ethers.keccak256(ethers.toUtf8Bytes("postgres"));
    await registry.connect(githubChild).attestWithProof(
      subject, credential, evidence, program, 0, [skill], Array(8).fill(0), [111, 80]
    );
    const attestationId = await registry.deriveAttestationId(subject, credential);
    const expiry = (await ethers.provider.getBlock("latest")).timestamp + 3600;

    await expect(
      registry.connect(outsider).bindHolder(attestationId, holder.address, expiry)
    ).to.be.revertedWithCustomError(registry, "UnauthorizedHolderBindingIssuer");

    await registry.connect(githubChild).bindHolder(attestationId, holder.address, expiry);
    expect(await registry.isHolderAuthorized(attestationId, holder.address)).to.equal(true);

    await registry.connect(githubChild).revokeHolderBinding(attestationId);
    expect(await registry.isHolderAuthorized(attestationId, holder.address)).to.equal(false);
  });
});
