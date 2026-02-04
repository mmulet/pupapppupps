import * as THREE from 'three';

/**
 * DogToySystem handles the spawning and simple physics of dog toys.
 * Uses built-in simple physics - no external physics library required.
 *
 * Example usage in player.html:
 *
 * <script type="module">
 *   import { DogToySystem } from './dog_toy.js';
 *   const dogToySystem = new DogToySystem(scene);
 *   // ... to throw a toy from camera...
 *   dogToySystem.throwToy(camera);
 *   // ... in animate loop ...
 *   dogToySystem.update(deltaTime);
 * </script>
 */
export class DogToySystem {
    constructor(scene) {
        this.scene = scene;
        this.toys = [];
        this.gravity = -15;
        this.bounceFactor = 0.5;
        this.friction = 0.95;
        this.groundY = 0.15; // Slightly above ground plane
    }

    /**
     * Creates a colorful dog toy mesh (ball with ring pattern)
     */
    createToyMesh() {
        const group = new THREE.Group();

        // Main ball
        const ballGeometry = new THREE.SphereGeometry(0.12, 16, 16);
        const ballMaterial = new THREE.MeshPhongMaterial({
            color: new THREE.Color().setHSL(Math.random(), 0.8, 0.5),
            shininess: 80
        });
        const ball = new THREE.Mesh(ballGeometry, ballMaterial);
        ball.castShadow = true;
        group.add(ball);

        // Add a ring around it (like a classic dog toy)
        const ringGeometry = new THREE.TorusGeometry(0.12, 0.03, 8, 16);
        const ringMaterial = new THREE.MeshPhongMaterial({
            color: new THREE.Color().setHSL(Math.random(), 0.9, 0.6),
            shininess: 60
        });
        const ring = new THREE.Mesh(ringGeometry, ringMaterial);
        ring.castShadow = true;
        ring.rotation.x = Math.PI / 2;
        group.add(ring);

        return group;
    }

    /**
     * Throws a toy from the camera position towards the center of the scene
     * @param {THREE.Camera} camera - The camera to throw from
     * @param {THREE.Vector3} [targetPos] - Optional target position (defaults to origin at y=1)
     */
    throwToy(camera, targetPos = null) {
        const toy = this.createToyMesh();

        // Start position: near the camera
        const startPos = new THREE.Vector3();
        camera.getWorldPosition(startPos);

        // Offset slightly in front of camera
        const forward = new THREE.Vector3(0, 0, -1);
        forward.applyQuaternion(camera.quaternion);
        startPos.add(forward.multiplyScalar(0.3));

        toy.position.copy(startPos);

        // Calculate throw direction towards target
        const target = targetPos || new THREE.Vector3(0, 1, 0);
        const direction = new THREE.Vector3().subVectors(target, startPos).normalize();

        // Initial velocity
        const throwSpeed = 6 + Math.random() * 2;
        const velocity = new THREE.Vector3(
            direction.x * throwSpeed,
            direction.y * throwSpeed + 4, // Add upward arc
            direction.z * throwSpeed
        );

        // Angular velocity for spinning
        const angularVelocity = new THREE.Vector3(
            (Math.random() - 0.5) * 10,
            (Math.random() - 0.5) * 10,
            (Math.random() - 0.5) * 10
        );

        this.scene.add(toy);
        this.toys.push({
            mesh: toy,
            velocity: velocity,
            angularVelocity: angularVelocity,
            age: 0,
            maxAge: 15, // Remove after 15 seconds
            settled: false
        });

        console.log('DogToySystem: Threw a toy!');
        return toy;
    }

    /**
     * Updates all toys physics - call this in your animation loop
     * @param {number} deltaTime - Time since last frame in seconds
     */
    update(deltaTime) {
        // Clamp deltaTime to prevent huge jumps
        deltaTime = Math.min(deltaTime, 0.1);

        const toRemove = [];

        for (let i = 0; i < this.toys.length; i++) {
            const toyData = this.toys[i];
            const toy = toyData.mesh;

            if (!toyData.settled) {
                // Apply gravity
                toyData.velocity.y += this.gravity * deltaTime;

                // Update position
                toy.position.x += toyData.velocity.x * deltaTime;
                toy.position.y += toyData.velocity.y * deltaTime;
                toy.position.z += toyData.velocity.z * deltaTime;

                // Apply angular velocity
                toy.rotation.x += toyData.angularVelocity.x * deltaTime;
                toy.rotation.y += toyData.angularVelocity.y * deltaTime;
                toy.rotation.z += toyData.angularVelocity.z * deltaTime;

                // Bounce off ground
                if (toy.position.y < this.groundY) {
                    toy.position.y = this.groundY;
                    toyData.velocity.y = -toyData.velocity.y * this.bounceFactor;

                    // Apply friction on bounce
                    toyData.velocity.x *= this.friction;
                    toyData.velocity.z *= this.friction;
                    toyData.angularVelocity.multiplyScalar(0.8);

                    // Check if settled
                    if (Math.abs(toyData.velocity.y) < 0.3 &&
                        toyData.velocity.length() < 0.5) {
                        toyData.settled = true;
                        toyData.velocity.set(0, 0, 0);
                        toyData.angularVelocity.set(0, 0, 0);
                    }
                }

                // Air resistance
                toyData.velocity.multiplyScalar(0.995);
            }

            // Age the toy
            toyData.age += deltaTime;

            // Fade out and remove old toys
            if (toyData.age > toyData.maxAge - 2) {
                const fadeProgress = (toyData.age - (toyData.maxAge - 2)) / 2;
                toy.traverse((child) => {
                    if (child.isMesh && child.material) {
                        child.material.transparent = true;
                        child.material.opacity = 1 - fadeProgress;
                    }
                });
            }

            if (toyData.age > toyData.maxAge) {
                toRemove.push(i);
            }
        }

        // Remove old toys (in reverse order)
        for (let i = toRemove.length - 1; i >= 0; i--) {
            const index = toRemove[i];
            const toyData = this.toys[index];
            this.scene.remove(toyData.mesh);
            toyData.mesh.traverse((child) => {
                if (child.geometry) child.geometry.dispose();
                if (child.material) child.material.dispose();
            });
            this.toys.splice(index, 1);
        }
    }

    /**
     * Removes all toys from the scene
     */
    clear() {
        for (const toyData of this.toys) {
            this.scene.remove(toyData.mesh);
            toyData.mesh.traverse((child) => {
                if (child.geometry) child.geometry.dispose();
                if (child.material) child.material.dispose();
            });
        }
        this.toys = [];
    }
}
