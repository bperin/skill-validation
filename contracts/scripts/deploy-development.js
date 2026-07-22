const { ethers } = require("hardhat");

async function main() {
  const Verifier = await ethers.getContractFactory("DevelopmentScoreVerifier");
  const verifier = await Verifier.deploy();
  await verifier.waitForDeployment();

  const Registry = await ethers.getContractFactory("SkillRegistry");
  const registry = await Registry.deploy(await verifier.getAddress());
  await registry.waitForDeployment();

  console.log("DEVELOPMENT ONLY verifier:", await verifier.getAddress());
  console.log("DEVELOPMENT ONLY registry:", await registry.getAddress());
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
