import * as THREE from 'three';
import { AmmoPhysics } from 'three/addons/physics/AmmoPhysics.js';

/**
 * DogToySystem handles the physics and spawning of dog toys.
 *
 * Note: This module requires 'ammo.wasm.js' (or compatible Ammo.js build) to be loaded
 * in the HTML via a <script> tag before use, as AmmoPhysics depends on the global 'Ammo' object.
 *
 * Example usage in player.html:
 *
 * <script src="path/to/ammo.wasm.js"></script>
 * <script type="module">
 *   import { DogToySystem } from './dog_toy.js';
 *   // ... inside init() ...
 *   // Pass existing physics world if you have one, or let it create one
 *   const dogToySystem = new DogToySystem(scene);
 *   await dogToySystem.init(existingPhysics);
 *   // ... to throw a toy ...
 *   dogToySystem.throwToy();
 * </script>
 */
export class DogToySystem {
    constructor(scene) {
        this.scene = scene;
        this.physics = null;
        this.toys = [];
    }

    /**
     * Initializes the physics world and sets up the ground collider.
     * @param {Object} [physicsWorld] - Optional existing AmmoPhysics instance.
     */
    async init(physicsWorld = null) {
        if (physicsWorld) {
            this.physics = physicsWorld;
        } else {
            if (typeof Ammo === 'undefined') {
                console.warn('DogToySystem: Ammo global is undefined. Please load ammo.wasm.js (or similar) in your HTML.');
            }
            this.physics = await AmmoPhysics();
        }

        // Create a floor collider that matches the ground in player.html.
        // The simple AmmoPhysics wrapper only supports BoxGeometry, SphereGeometry, and IcosahedronGeometry.
        // We use a BoxGeometry for the floor.
        const floorGeometry = new THREE.BoxGeometry(100, 5, 100);
        const floorMaterial = new THREE.ShadowMaterial({ opacity: 0.1 });
        const floor = new THREE.Mesh(floorGeometry, floorMaterial);

        // Position the floor so its top surface is at y=0.
        // Height is 5, so center should be at y = -2.5.
        floor.position.y = -2.5;
        floor.receiveShadow = true;

        // Mark as static object (mass: 0)
        floor.userData.physics = { mass: 0 };

        this.scene.add(floor);

        // Explicitly add the floor to the physics world.
        if (this.physics && this.physics.addMesh) {
            this.physics.addMesh(floor, 0);
        }

        console.log('DogToySystem: Physics initialized');
    }

    /**
     * Spawns a new dog toy (cube) into the scene with physics enabled.
     */
    throwToy() {
        if (!this.physics) {
            console.warn('DogToySystem: Physics not initialized. Call init() first.');
            return;
        }

        const size = 0.3; // Size of the toy
        const geometry = new THREE.BoxGeometry(size, size, size);
        const material = new THREE.MeshStandardMaterial({
            color: Math.random() * 0xffffff,
            roughness: 0.6,
            metalness: 0.2
        });

        const toy = new THREE.Mesh(geometry, material);

        // Start position: randomly around center, high up
        const x = (Math.random() - 0.5) * 4;
        const z = (Math.random() - 0.5) * 4;
        const y = 5 + Math.random() * 2;

        toy.position.set(x, y, z);

        // Random rotation
        toy.rotation.set(
            Math.random() * Math.PI,
            Math.random() * Math.PI,
            Math.random() * Math.PI
        );

        toy.castShadow = true;
        toy.receiveShadow = true;

        // Physics properties
        toy.userData.physics = { mass: 1 };

        this.scene.add(toy);
        this.toys.push(toy);

        // Add the new mesh to the physics world
        this.physics.addMesh(toy, 1);

        return toy;
    }
}
